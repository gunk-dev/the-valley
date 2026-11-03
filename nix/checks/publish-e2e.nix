# An integrator landing, published: one NixOS machine running the module
# with a controller, the bus and a push mirror, and a change that lands by
# integration rather than by push. The landing has to reach the mirror and
# the bus exactly as a push does, though the integrator moves the ref with
# update-ref under its own user, and post-receive never runs. The mirror is
# missing at first. The landing's event has to reach the bus anyway, at
# once, and the landing has to stay queued through the failed mirror push
# until a later run of the drain publishes it. The test starts that run
# itself: when systemd retries a failed run is the module's restart
# settings, which module-eval holds. The host also publishes, in its
# instance repository, the signers a client verifies the landing with, and
# publishes them again when the signers file is replaced. No push over ssh
# replaces that publication or deletes it. The controllers pass on a
# one-hour interval, so every landing here is started by a mark in a
# controller's wake directory: the host's hook on a push, and the instance
# controller's own mark after it lands the floor.
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
  # trusts them exists for one test run. forged_signer is the contributor's
  # key under the integrator's name, what a forged publication would hold.
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
        attest key --key "$out/integrator" --name host/attestations > "$out/integrator_signer"
        attest key --key "$out/contributor" --name host/attestations > "$out/forged_signer"
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
      # The signers file is a copy made at boot, at a path the test
      # replaces as the registry compiler replaces its own file.
      systemd.tmpfiles.rules = [
        "d /var/lib/valley-signers-file 0755 root root - -"
        "C /var/lib/valley-signers-file/known_signers 0644 root root - ${keys}/known_signers"
      ];
      services.valley = {
        enable = true;
        config = declaration;
        authorizedKeys = [ (lib.fileContents "${keys}/contributor.pub") ];
        bus.enable = true;
        integrator = {
          enable = true;
          signingKeyFile = "${keys}/integrator";
          knownSignersFile = "/var/lib/valley-signers-file/known_signers";
          instanceProject = "instance";
          interval = "1h";
        };
      };
    };

    testScript = ''
      import json
      import time

      host.wait_for_unit("multi-user.target")
      host.wait_for_unit("valley-publish@project.path")
      host.wait_until_succeeds("test -d /srv/git/project.git/valley-publish-queue")

      # The repositories belong to the git user, so git reads them as it.
      git = "sudo -u git git"
      queue = "/srv/git/project.git/valley-publish-queue"

      def unit(prop):
          return host.succeed(f"systemctl show -p {prop} --value valley-publish@project").strip()

      # Every payload on the bus, as published.
      def payloads():
          out = host.succeed(
              "n=$(nats --server nats://127.0.0.1:4222 stream info valley --json | jq .state.messages);"
              " for i in $(seq 1 $n); do"
              "   nats --server nats://127.0.0.1:4222 stream get valley $i --json | jq -r .data | base64 -d; echo;"
              " done"
          )
          return [line for line in out.splitlines() if line.strip()]

      def events():
          return [json.loads(line) for line in payloads()]

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
      # The request ref alone files the change for integration.
      seed("project", f"{change}:refs/the-valley/integration-requests/main/one")

      # The fetch ran no hook, so nothing marked the controller. The hook the
      # host installed is run by hand as the git user, as a push runs it:
      # moves of the integrator's own bookkeeping mark nothing, and the
      # request's move marks the project's controller, which lands it at
      # once on its one-hour interval.
      wakes = "/run/valley-integrator-wake"
      hook = "/srv/git/project.git/hooks/post-receive.d/valley-integrator-wake"
      zero = "0" * 40

      def due_passes(project):
          return int(host.succeed(f"journalctl -u valley-integrator@{project} -o cat | grep -c '^pass: due$' || true").strip())

      with subtest("the host's hook marks the controller for what it reads, and only that"):
          host.succeed(f"test -L {hook}")
          before = due_passes("project")
          host.succeed(
              f"printf '%s %s %s\\n' {zero} {change} refs/the-valley/integration-outcomes/main/one"
              f" {zero} {change} refs/the-valley/knots | sudo -u git {hook}"
          )
          host.sleep(2)
          assert due_passes("project") == before, "bookkeeping marked the controller"
          marked = time.monotonic()
          host.succeed(
              f"printf '%s %s %s\\n' {zero} {change} refs/the-valley/integration-requests/main/one | sudo -u git {hook}"
          )

      # The change lands as one commit the integrator makes on the base,
      # with the change's tree. That commit is what main, the mirror and
      # the events carry from here on.
      with subtest("the landing moves main"):
          host.wait_until_succeeds(
              f"test \"$({git} -C /srv/git/project.git rev-parse refs/heads/main^)\" = {base}", timeout=120
          )
          took = time.monotonic() - marked
          print(f"mark to landing, one-hour interval, VM: {took:.1f}s")
          assert took < 60, took
          assert due_passes("project") > before
          landing = host.succeed(f"{git} -C /srv/git/project.git rev-parse refs/heads/main").strip()
          assert landing != change, "the change's own commit landed"
          tree = f"{git} -C /srv/git/project.git rev-parse"
          assert host.succeed(f"{tree} {landing}^{{tree}}") == host.succeed(f"{tree} {change}^{{tree}}")

      with subtest("a failed mirror push keeps the move queued and fails the run"):
          host.wait_until_succeeds(
              "journalctl -t valley-mirror | grep -q 'project: push to /srv/mirror.git FAILED'", timeout=60
          )
          host.wait_until_succeeds("test \"$(systemctl show -p SubState --value valley-publish@project)\" = auto-restart")
          host.succeed(f"test -n \"$(ls -A {queue})\"")

      # The integrator publishes the event itself, so the mirror being down
      # does not hold it back. Its payload is byte for byte the one the host
      # publishes for a push of the same move.
      with subtest("the landing is on the bus while the mirror is down"):
          host.wait_until_succeeds(
              "nats --server nats://127.0.0.1:4222 stream get valley --last-for valley.git.project.ref-updated"
          )
          host.succeed(f"test -n \"$(ls -A {queue})\"")
          moved = [p for p in payloads() if '"event":"ref-updated"' in p and '"ref":"refs/heads/main"' in p]
          assert moved == [
              f'{{"event":"ref-updated","repo":"project","ref":"refs/heads/main","old":"{base}","new":"{landing}"}}'
          ], moved

      with subtest("once the mirror is there, the next run pushes the queued move and empties the queue"):
          host.succeed(
              "mkdir /srv/mirror.git && chown git:git /srv/mirror.git",
              f"{git} init --quiet --bare /srv/mirror.git",
              "systemctl restart valley-publish@project",
          )
          assert host.succeed(f"{git} -C /srv/mirror.git rev-parse refs/heads/main").strip() == landing
          host.wait_until_succeeds("journalctl -t valley-mirror | grep -q 'project: pushed to /srv/mirror.git'")
          host.succeed(f"test -z \"$(ls -A {queue})\"")
          assert unit("ActiveState") == "inactive"

      with subtest("a move that cannot be deleted fails the run and stays queued, and the next run empties the queue"):
          # A directory in the queue is a move rm -f cannot delete.
          host.succeed(f"sudo -u git mkdir {queue}/stuck", f"sudo -u git touch {queue}/stuck/x")
          host.wait_until_succeeds(
              "journalctl -u valley-publish@project | grep -q 'cannot remove .*stuck'", timeout=60
          )
          host.wait_until_succeeds("test \"$(systemctl show -p SubState --value valley-publish@project)\" = auto-restart")
          host.succeed(f"test -e {queue}/stuck/x")
          host.succeed("systemctl is-active valley-publish@project.path")
          host.succeed(f"rm -r {queue}/stuck", "systemctl restart valley-publish@project")
          host.succeed(f"test -z \"$(ls -A {queue})\"")
          assert unit("ActiveState") == "inactive"

      with subtest("the landing is one ref-updated event on the bus"):
          moved = [e for e in events() if e["event"] == "ref-updated" and e["ref"] == "refs/heads/main"]
          assert moved == [
              {"event": "ref-updated", "repo": "project", "ref": "refs/heads/main", "old": base, "new": landing}
          ], moved
          landed = [e for e in events() if e["event"] == "integration-succeeded"]
          assert len(landed) == 1 and landed[0]["new"] == landing, landed

      # What a client verifies the landing with, in the instance repository:
      # the controllers' signers file, and the name they sign under, which
      # this host leaves to the default, the machine's own name. The file
      # replaced by a rename, as the registry compiler replaces its own, is
      # published again with no activation.
      with subtest("the host publishes the controllers' signers, and again when the file changes"):
          ref = "refs/the-valley/signers"
          instance = f"{git} -C /srv/git/instance.git"
          signers = "/var/lib/valley-signers-file/known_signers"
          host.succeed(
              f"test \"$({instance} cat-file -t {ref})\" = tree",
              f"test \"$({instance} cat-file blob {ref}:integrator-name)\" = host/attestations",
              f"{instance} cat-file blob {ref}:known-signers | cmp - {signers}",
          )
          host.succeed(
              f"cat {signers} ${keys}/integrator_signer > /var/lib/valley-signers-file/next",
              f"mv -f /var/lib/valley-signers-file/next {signers}",
          )
          host.wait_until_succeeds(f"{instance} cat-file blob {ref}:known-signers | cmp - {signers}", timeout=60)
          host.succeed(f"{instance} cat-file blob {ref}:known-signers | grep -q '^host/attestations+'")

      # Only the host's machinery writes the publication. A push over ssh,
      # by a key the host authorizes, cannot replace it with a well-formed
      # publication of a key the pusher holds, and cannot delete it.
      with subtest("no push over ssh replaces or deletes the publication"):
          host.wait_for_unit("sshd.service")
          published = host.succeed(f"{instance} rev-parse {ref}").strip()
          host.succeed(
              "mkdir -p /root/keys",
              "install -m 600 ${keys}/contributor /root/keys/contributor",
              "git init --quiet --bare /root/forged.git",
          )
          forged = host.succeed(
              "cd /root/forged.git"
              " && name=$(echo host/attestations | git hash-object -w --stdin)"
              " && file=$(git hash-object -w ${keys}/forged_signer)"
              " && printf '100644 blob %s\\tintegrator-name\\n100644 blob %s\\tknown-signers\\n' $name $file | git mktree"
          ).strip()
          ssh = (
              "GIT_SSH_COMMAND='ssh -i /root/keys/contributor -o IdentitiesOnly=yes -o BatchMode=yes"
              " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'"
          )
          for refspec in [f"+{forged}:{ref}", f":{ref}"]:
              out = host.fail(f"cd /root/forged.git && {ssh} git push git@localhost:instance.git {refspec} 2>&1")
              assert f"{ref} is written only by the machinery on the host, and no push may write it" in out, out
              assert host.succeed(f"{instance} rev-parse {ref}").strip() == published, refspec

      # The wake directories are the host's: one per controller, owned by
      # the integrator's user and the writers' group, which is the git user
      # and the integrator's user. An idle one holds nothing.
      with subtest("each controller's wake directory is its own, and a writer group's"):
          for project in ["instance", "project"]:
              assert host.succeed(f"stat -c '%a %U %G' {wakes}/{project}").strip() == "770 valley-integrator valley-integrator-wake"
              host.wait_until_succeeds(f"test -z \"$(ls -A {wakes}/{project})\"")
          members = host.succeed("getent group valley-integrator-wake").strip().split(":")[3].split(",")
          assert sorted(members) == ["git", "valley-integrator"], members

      # A push over ssh runs the dispatcher, whose hooks run in turn. A topic
      # branch, which anyone may push, marks the project's controller and
      # not the instance's.
      with subtest("a push over ssh wakes the controller of the project it moved, and no other"):
          host.succeed(
              f"git init --quiet /root/topic && cd /root/topic && {commit} --allow-empty -m topic"
          )
          project_before, instance_before = due_passes("project"), due_passes("instance")
          host.succeed(f"cd /root/topic && {ssh} git push --quiet git@localhost:project.git HEAD:refs/heads/topic")
          host.wait_until_succeeds(f"test {project_before} -lt $(journalctl -u valley-integrator@project -o cat | grep -c '^pass: due$')", timeout=30)
          host.sleep(2)
          assert due_passes("instance") == instance_before, "a push to project woke the instance's controller"

      # The floor moves by the instance's controller, which runs no hook. It
      # marks the project's controller itself, from inside its sandbox, after
      # the landing. The request is fetched, so only a push to a topic branch
      # of the instance marks the instance's controller.
      with subtest("a floor landed by the instance's controller wakes the controller that reads it"):
          floor_base = host.succeed(f"{git} -C /srv/git/instance.git rev-parse refs/heads/main").strip()
          host.succeed(
              f"cd /tmp/work && sudo -u git git checkout --quiet -b floor {floor_base}",
              "cd /tmp/work && sudo -u git sh -c 'echo moved > moved.txt' && sudo -u git git add moved.txt",
              f"cd /tmp/work && sudo -u git {commit} -m 'the floor, moved'",
          )
          seed("instance", "floor:refs/the-valley/integration-requests/main/floor")
          before = due_passes("project")
          host.succeed(f"cd /root/topic && {ssh} git push --quiet git@localhost:instance.git HEAD:refs/heads/topic")
          host.wait_until_succeeds(
              f"test \"$({git} -C /srv/git/instance.git rev-parse refs/heads/main^)\" = {floor_base}", timeout=60
          )
          host.wait_until_succeeds(
              f"journalctl -u valley-integrator@instance -o cat | grep -q '^  wake     the floor reader at {wakes}/project is due$'",
              timeout=30,
          )
          host.wait_until_succeeds(f"test {before} -lt $(journalctl -u valley-integrator@project -o cat | grep -c '^pass: due$')", timeout=30)
    '';
  };
}
