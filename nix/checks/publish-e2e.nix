# An integrator landing, published: one NixOS machine running the module
# with a controller, the bus and a push mirror, and a change that lands by
# integration rather than by push. The landing has to reach the mirror and
# the bus exactly as a push does, though the integrator moves the ref with
# update-ref under its own user, and post-receive never runs. The mirror is
# missing at first, so the landing also has to survive a failed mirror push
# and be published by the retry, which waits for the project's publish lock.
{
  pkgs,
  lib,
  self,
  packages,
  ...
}:
let
  # Throwaway keys, made once at build time, and the signers file the
  # controller is given. They hold no authority anywhere: the machine that
  # trusts them exists for one test run.
  keys =
    pkgs.runCommand "valley-publish-e2e-keys"
      {
        nativeBuildInputs = [
          pkgs.openssh
          packages.attest
        ];
      }
      ''
        mkdir $out
        for who in integrator contributor; do
          ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$out/$who"
        done
        attest key --key "$out/contributor" --name contributor/attestations > "$out/known_signers"
      '';

  declaration = pkgs.writeText "valley-publish-e2e.cue" ''
    package valley

    projects: {
      // The instance repository, which carries the floor.
      "instance": protection: refs: ["refs/heads/main"]
      "project": {
        protection: {}
        mirrors: ["/srv/mirror.git"]
      }
    }
  '';

  # A floor that requires nothing, so a change lands on the strength of its
  # delta applying, and the test needs no evidence and no nix evaluation.
  floor = pkgs.writeText "floor.cue" ''
    package verification

    floor: {
    	checks: {}
    	classes: {}
    	unclassified: {}
    }
  '';
in
{
  publish-e2e = pkgs.testers.runNixOSTest {
    name = "valley-publish-e2e";
    nodes.host = {
      imports = [ self.nixosModules.default ];
      environment.systemPackages = [
        pkgs.git
        pkgs.natscli
        pkgs.jq
      ];
      services.valley = {
        enable = true;
        config = declaration;
        authorizedKeys = [ (lib.fileContents "${keys}/contributor.pub") ];
        bus.enable = true;
        integrator = {
          enable = true;
          signingKeyFile = "${keys}/integrator";
          knownSignersFile = "${keys}/known_signers";
          instanceProject = "instance";
          interval = "2s";
        };
      };
    };

    testScript = ''
      import json

      host.wait_for_unit("multi-user.target")
      host.wait_for_unit("valley-publish@project.path")
      host.wait_until_succeeds("test -d /srv/git/project.git/valley-publish-queue")

      # The repositories belong to the git user, so git reads them as it.
      git = "sudo -u git git"
      queue = "/srv/git/project.git/valley-publish-queue"
      lock = "/srv/git/project.git/valley-publish.flock"

      # The mirror does not exist yet, so the first push to it fails.

      # Two commits: the floor, which is main on both repositories, and a
      # change one commit ahead of it.
      commit = "git -c user.name=valley-check -c user.email=valley-check@localhost commit --quiet"
      host.succeed(
          "mkdir -p /tmp/work/policy/instance",
          "cp ${floor} /tmp/work/policy/instance/floor.cue",
          f"cd /tmp/work && git init --quiet --initial-branch=main && git add -A && {commit} -m floor",
          f"cd /tmp/work && echo change > change.txt && git add -A && {commit} -m change",
      )
      base = host.succeed("git -C /tmp/work rev-parse HEAD~1").strip()
      change = host.succeed("git -C /tmp/work rev-parse HEAD").strip()
      host.succeed("chown -R git:git /tmp/work")

      # Seeded by fetching into the served repositories. A fetch runs no
      # hook, so nothing here is published, and the mirror and the bus see
      # only what the landing does.
      def seed(repo, refspec):
          host.succeed(f"{git} -C /srv/git/{repo}.git fetch --quiet /tmp/work {refspec}")

      seed("instance", f"{base}:refs/heads/main")
      seed("project", f"{base}:refs/heads/main")
      host.fail(f"{git} -C /srv/mirror.git rev-parse --verify --quiet refs/heads/main")
      # The request ref alone asks for the change to land.
      seed("project", f"{change}:refs/the-valley/integration-requests/main/one")

      with subtest("the landing moves main"):
          host.wait_until_succeeds(
              f"test \"$({git} -C /srv/git/project.git rev-parse refs/heads/main)\" = {change}", timeout=120
          )

      with subtest("a failed mirror push keeps the move queued and fails the run"):
          host.wait_until_succeeds(
              "journalctl -t valley-mirror | grep -q 'project: push to /srv/mirror.git FAILED'", timeout=60
          )
          # The move went to the bus, so it is marked sent, and it stays.
          host.succeed(f"ls {queue} | grep -q '[.]sent$'")

      with subtest("the retry waits for the publish lock"):
          # Held here, as the git user, between two runs: while the unit waits
          # to restart, the lock is free.
          host.succeed(
              "systemd-run --unit=hold-publish-lock -p User=git"
              f" ${pkgs.util-linux}/bin/flock {lock} ${pkgs.runtimeShell}"
              " -c '${pkgs.coreutils}/bin/touch /tmp/publish-lock-held; exec ${pkgs.coreutils}/bin/sleep infinity'"
          )
          host.wait_for_file("/tmp/publish-lock-held", timeout=30)
          host.succeed(
              "mkdir /srv/mirror.git && chown git:git /srv/mirror.git",
              f"{git} init --quiet --bare /srv/mirror.git",
          )
          # systemd, not the path unit, starts the retry, after its delay.
          host.wait_until_succeeds(
              "test \"$(systemctl show -p SubState --value valley-publish@project)\" = start", timeout=90
          )
          restarts = host.succeed("systemctl show -p NRestarts --value valley-publish@project").strip()
          assert restarts == "1", restarts
          host.sleep(3)
          host.fail(f"{git} -C /srv/mirror.git rev-parse --verify --quiet refs/heads/main")
          host.succeed(f"test -n \"$(ls -A {queue})\"")
          host.succeed("systemctl stop hold-publish-lock")

      with subtest("the retry reaches the mirror, pushed as the git user"):
          host.wait_until_succeeds(
              f"test \"$({git} -C /srv/mirror.git rev-parse refs/heads/main)\" = {change}", timeout=60
          )
          host.wait_until_succeeds("journalctl -t valley-mirror | grep -q 'project: pushed to /srv/mirror.git'")
          host.wait_until_succeeds(f"test -z \"$(ls -A {queue})\"")
          host.wait_until_succeeds("test \"$(systemctl show -p ActiveState --value valley-publish@project)\" = inactive")

      def events():
          out = host.succeed(
              "n=$(nats --server nats://127.0.0.1:4222 stream info valley --json | jq .state.messages);"
              " for i in $(seq 1 $n); do"
              "   nats --server nats://127.0.0.1:4222 stream get valley $i --json | jq -r .data | base64 -d; echo;"
              " done"
          )
          return [json.loads(line) for line in out.splitlines() if line.strip()]

      # Once, though the drain ran twice: a retry does not resend to the bus.
      with subtest("the landing is one ref-updated event on the bus"):
          host.wait_until_succeeds(
              "nats --server nats://127.0.0.1:4222 stream get valley --last-for valley.git.project.ref-updated"
          )
          moved = [e for e in events() if e["event"] == "ref-updated"]
          assert moved == [
              {"event": "ref-updated", "repo": "project", "ref": "refs/heads/main", "old": base, "new": change}
          ], moved
          landed = [e for e in events() if e["event"] == "integration-succeeded"]
          assert len(landed) == 1 and landed[0]["new"] == change, landed
    '';
  };
}
