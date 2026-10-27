# The restacker as a host runs it: one NixOS machine running the module
# with services.valley.restacker on for one project. The operator's two
# deployment steps are taken here as a deployment takes them: the
# restacker's key is declared under its principal with the restack grant,
# and its private key is provisioned outside the store, readable by root
# alone. The controller then restacks a ready change after main moves,
# pushing over this host's sshd as its own principal, and its Unix user
# holds nothing else: no git group, no repository on disk, no integrator
# key. Over ssh its key can push restack versions and reports, and still
# cannot push main, file a request, delete a record or write an outcome.
{
  pkgs,
  lib,
  self,
  packages,
  ...
}:
let
  # Throwaway keys, made once at build time. They hold no authority
  # anywhere: the machine that trusts them exists for one test run.
  keys = pkgs.runCommand "valley-restack-host-e2e-keys" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    for who in alice restacker integrator; do
      ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$out/$who"
    done
  '';
  pub = who: lib.fileContents "${keys}/${who}.pub";

  # main is protected and only the integrator lands on it.
  declaration = pkgs.writeText "valley-restack-host-e2e.cue" ''
    package valley

    projects: "app": protection: {}
  '';
in
{
  restack-host-e2e = pkgs.testers.runNixOSTest {
    name = "valley-restack-host-e2e";
    nodes.host = {
      imports = [ self.nixosModules.default ];
      environment.systemPackages = [
        pkgs.git
        pkgs.jq
        packages.valley
      ];
      services.valley = {
        enable = true;
        config = declaration;
        # The operator's registration: the restacker's key under its
        # principal, and the restack grant.
        authorizedKeys = [
          {
            principal = "alice";
            key = pub "alice";
          }
          {
            principal = "valley-restacker";
            key = pub "restacker";
          }
        ];
        grants.restack = [ "valley-restacker" ];
        integrator = {
          enable = true;
          signingKeyFile = "/run/valley-keys/valley-integrator";
          knownSignersFile = pkgs.writeText "valley-restack-host-e2e-signers" "";
          instancePolicy = "${../../examples/policy/smoke/instance}";
        };
        restacker = {
          enable = true;
          projects = [ "app" ];
          sshKeyFile = "/run/valley-keys/valley-restacker";
          interval = "1s";
        };
      };
    };

    testScript = ''
      host.wait_for_unit("multi-user.target")
      host.wait_for_unit("sshd.service")
      host.wait_until_succeeds("test -L /srv/git/app.git/hooks/pre-receive")

      # The repositories belong to the git user, so git reads them as it.
      git = "sudo -u git git"
      commit = "git -c user.name=valley-check -c user.email=valley-check@localhost commit --quiet"
      def ssh(who):
          return (
              f"GIT_SSH_COMMAND='ssh -i /root/keys/{who} -o IdentitiesOnly=yes -o BatchMode=yes"
              " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'"
          )

      # main moves by a local write as the git user, which runs no hook, as
      # the integrator lands a change.
      def land(name):
          host.succeed(
              f"echo {name} > /tmp/app/{name}",
              f"{git} -C /tmp/app add {name}",
              f"{git} -C /tmp/app -c user.name=valley-check -c user.email=valley-check@localhost commit --quiet -m {name}",
              f"{git} -C /srv/git/app.git fetch --quiet /tmp/app main:refs/heads/main",
          )

      with subtest("the operator provisions the keys, outside the store"):
          # The restacker's is readable by root alone: systemd reads it,
          # and hands it to each controller as a credential. The
          # integrator's is its own user's.
          host.succeed(
              "install -D -m 0400 -o root -g root ${keys}/restacker /run/valley-keys/valley-restacker",
              "install -D -m 0400 -o valley-integrator -g root ${keys}/integrator /run/valley-keys/valley-integrator",
              "systemctl restart valley-integrator@app.service",
          )

      with subtest("the project is seeded, and alice readies a change"):
          host.succeed(
              "mkdir -p /root/keys && install -m 600 ${keys}/alice ${keys}/restacker /root/keys/",
              "mkdir -p /tmp/app/policy/project",
              "printf 'package verification\\n\\nproject: restack: maxAutomaticVersions: 2\\n' > /tmp/app/policy/project/policy.cue",
              f"cd /tmp/app && git init --quiet --initial-branch=main && git add -A && {commit} -m seed",
              "chown -R git:git /tmp/app",
              f"{git} -C /srv/git/app.git fetch --quiet /tmp/app main:refs/heads/main",
              f"{ssh('alice')} git clone --quiet git@localhost:app.git /root/app",
              "git -C /root/app config valley.principal alice",
              "git -C /root/app config user.name alice",
              "git -C /root/app config user.email alice@localhost",
              f"cd /root/app && git checkout --quiet -b a && echo a > a && git add a && {commit} -m a",
              f"cd /root/app && {ssh('alice')} git push --quiet origin a",
              f"cd /root/app && {ssh('alice')} valley ready a",
          )
          change = host.succeed(
              f"{git} -C /srv/git/app.git for-each-ref --format='%(refname:lstrip=4)' refs/the-valley/changes/main/"
          ).strip()
          assert len(change) == 32, change
          record = f"refs/the-valley/changes/main/{change}"

      with subtest("after main moves, the controller restacks the change as its own principal"):
          # Restarted, so the backoff of the passes that failed before main
          # existed starts over.
          host.succeed("systemctl restart valley-restacker@app.service")
          host.wait_for_unit("valley-restacker@app.service")
          land("landed")
          host.wait_until_succeeds(
              f"{git} -C /srv/git/app.git cat-file blob {record}:record.json"
              " | jq -e '.seq == 2 and .active == 2 and .owner == \"alice\" and .versions[1].uploader == \"valley-restacker\"'",
              timeout=120,
          )
          head = host.succeed(f"{git} -C /srv/git/app.git rev-parse refs/heads/a").strip()
          main = host.succeed(f"{git} -C /srv/git/app.git rev-parse refs/heads/main").strip()
          host.succeed(f"test \"$({git} -C /srv/git/app.git rev-parse {head}^)\" = {main}")
          host.wait_until_succeeds(
              f"{git} -C /srv/git/app.git cat-file blob refs/the-valley/restack/main/{change}:report.json"
              " | jq -e '.result == \"complete\"'",
              timeout=60,
          )

      with subtest("the controller's Unix user holds nothing but its own state"):
          host.succeed(
              "test \"$(systemctl show -p User --value valley-restacker@app.service)\" = valley-restacker",
              "test \"$(id -Gn valley-restacker)\" = valley-restacker",
              "test \"$(stat -c '%U %a' /var/lib/valley-restacker/app)\" = 'valley-restacker 700'",
          )
          for denied in [
              # The served repositories, on disk.
              "ls /srv/git",
              "git -C /srv/git/app.git rev-parse refs/heads/main",
              "touch /srv/git/app.git/refs/heads/written",
              # The integrator's key and state.
              "cat /run/valley-keys/valley-integrator",
              "ls /var/lib/valley-integrator",
              # Its own key file, which only systemd reads, as root.
              "cat /run/valley-keys/valley-restacker",
          ]:
              host.fail(f"sudo -u valley-restacker {denied}")
          host.fail("sudo -u valley-integrator cat /run/credentials/valley-restacker@app.service/key")

      with subtest("over ssh its key pushes as its principal, and the hook still closes what the grant does not give"):
          host.succeed(
              f"cd /root/app && {ssh('restacker')} git fetch --quiet origin",
              "cd /root/app && git checkout --quiet -B probe origin/main && git commit --quiet --allow-empty -m probe",
          )
          for refused in [
              "probe:refs/heads/main",
              f"probe:refs/the-valley/integration-requests/main/c-{change}",
              "probe:refs/the-valley/integration-outcomes/main/zz-probe",
              f":{record}",
          ]:
              host.fail(f"cd /root/app && {ssh('restacker')} git push --quiet origin {refused}")
          # A topic branch, as any named principal may push.
          host.succeed(f"cd /root/app && {ssh('restacker')} git push --quiet origin probe:refs/heads/probe")
          host.succeed(f"test \"$({git} -C /srv/git/app.git rev-parse refs/heads/main)\" = {main}")
          host.succeed(f"test -z \"$({git} -C /srv/git/app.git for-each-ref refs/the-valley/integration-requests/ refs/the-valley/integration-outcomes/)\"")
    '';
  };
}
