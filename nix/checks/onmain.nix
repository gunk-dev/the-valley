# The onMain bridge (onmain/): its unit tests, and one NixOS machine for
# the module's wiring around it.
{
  pkgs,
  lib,
  self,
  packages,
  ...
}:
let
  # A throwaway key the machine authorizes for one test run.
  keys = pkgs.runCommand "valley-onmain-e2e-keys" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    ssh-keygen -q -t ed25519 -N "" -C pusher -f "$out/pusher"
  '';

  declaration = pkgs.writeText "valley-onmain-e2e.cue" ''
    package valley

    projects: app: {}
  '';
in
{
  # The bridge over real git repositories: a loose ref replaced, packed-refs
  # rewritten, a missing refs/heads watched for, the tip requested at start
  # and not again while it stands, a lost watch set up again, and reftable
  # refused; the handoff to the unit, which runs a commit requested at any
  # moment of a run; and the unit's environment written from nothing but
  # one commit ID, with the request consumed whatever the commit file holds.
  onmain-unit = packages.onmain.unit;

  # What the module wires, on a machine, with systemd's own path units: a
  # push over ssh to main starts the bound unit, which reads the commit and
  # the repository from the environment its first command wrote from the
  # bridge's commit. A run that fails stays failed, with no request left
  # and the path unit waiting, and the next push runs it. Moves during a
  # run, and a restart of the bridge then, become one run at the newest
  # tip once it ends. The bridge's user has no grant to start, stop or
  # restart any unit, and cannot add an assignment to the unit's
  # environment, or write where it is kept. A start the unit's start limit
  # refuses stops the path unit, and resetting both runs the newest tip.
  onmain-e2e = pkgs.testers.runNixOSTest {
    name = "valley-onmain-e2e";
    nodes.host = {
      imports = [ self.nixosModules.default ];
      environment.systemPackages = [ pkgs.git ];
      services.valley = {
        enable = true;
        config = declaration;
        authorizedKeys = [ (lib.fileContents "${keys}/pusher.pub") ];
        onMain.converge = {
          project = "app";
          unit = "converge.service";
        };
        onMain.limited = {
          project = "app";
          unit = "limited.service";
        };
      };
      # The bound unit records what it was started for. It fails when fail
      # exists as it starts, and waits while hold, a FIFO, is there until
      # the test writes to it. The test starts it more often than the
      # default start limit allows, so the host lifts the limit, as a host
      # whose project lands faster than that would.
      systemd.services.converge = {
        startLimitIntervalSec = 0;
        serviceConfig = {
          Type = "oneshot";
          StateDirectory = "converge";
        };
        script = ''
          echo "$VALLEY_COMMIT $VALLEY_REPOSITORY" >> /var/lib/converge/runs
          failing=
          if [ -e /var/lib/converge/fail ]; then failing=1; fi
          if [ -p /var/lib/converge/hold ]; then read -r _ < /var/lib/converge/hold; fi
          test -z "$failing"
        '';
      };
      # A bound unit whose start limit allows one start an hour.
      systemd.services.limited = {
        startLimitBurst = 1;
        startLimitIntervalSec = 3600;
        serviceConfig = {
          Type = "oneshot";
          StateDirectory = "limited";
        };
        script = ''
          echo "$VALLEY_COMMIT" >> /var/lib/limited/runs
        '';
      };
      systemd.services.other = {
        serviceConfig.Type = "oneshot";
        script = "true";
      };
    };

    testScript = ''
      host.wait_for_unit("multi-user.target")
      host.wait_for_unit("valley-init.service")
      host.wait_for_unit("valley-on-main-converge.service")
      host.wait_for_unit("valley-on-main-converge.path")
      host.wait_for_unit("sshd.service")

      runs = "/var/lib/converge/runs"
      request = "/run/valley-on-main-converge/request"
      ssh = (
          "GIT_SSH_COMMAND='ssh -i /root/pusher -o IdentitiesOnly=yes -o BatchMode=yes"
          " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'"
      )
      commit = "git -c user.name=valley-check -c user.email=valley-check@localhost commit --quiet --allow-empty"
      host.succeed(
          "install -m 600 ${keys}/pusher /root/pusher",
          "git init --quiet --initial-branch=main /root/work",
      )

      def push(message):
          host.succeed(f"cd /root/work && {commit} -m {message} && {ssh} git push --quiet git@localhost:app.git HEAD:refs/heads/main")
          return host.succeed("git -C /root/work rev-parse HEAD").strip()

      def recorded():
          return host.succeed(f"cat {runs} 2>/dev/null || true").splitlines()

      def show(unit, prop):
          return host.succeed(f"systemctl show -P {prop} {unit}").strip()

      # Waits for the nth run to be recorded, the unit to stop, and the
      # path unit to wait again: once it waits, it has looked for a
      # request and found none, so nothing starts the unit until one is
      # made.
      def settled(n):
          host.wait_until_succeeds(
              f"test \"$(wc -l < {runs})\" -ge {n}"
              " && systemctl show -P ActiveState converge.service | grep -qxE 'inactive|failed'"
              " && test \"$(systemctl show -P SubState valley-on-main-converge.path)\" = waiting",
              timeout=60,
          )
          assert len(recorded()) == n, recorded()
          host.succeed(f"test ! -e {request}")

      with subtest("a push to main starts the bound unit with the commit and the repository"):
          host.succeed(f"test ! -e {runs}")
          first = push("first")
          settled(1)
          assert recorded() == [f"{first} /srv/git/app.git"], recorded()
          assert show("converge.service", "Result") == "success"

      with subtest("a failed run stays failed, with no request left and neither limit hit"):
          host.succeed("touch /var/lib/converge/fail")
          second = push("second")
          settled(2)
          assert recorded()[1:] == [f"{second} /srv/git/app.git"], recorded()
          assert show("converge.service", "ActiveState") == "failed"
          assert show("converge.service", "Result") == "exit-code"
          assert show("valley-on-main-converge.path", "ActiveState") == "active"
          assert show("valley-on-main-converge.path", "Result") == "success"
          host.succeed("systemctl is-active valley-on-main-converge.service")
          host.succeed("rm /var/lib/converge/fail")
          third = push("third")
          settled(3)
          assert recorded()[2:] == [f"{third} /srv/git/app.git"], recorded()
          assert show("converge.service", "Result") == "success"

      with subtest("moves during a failing run, and a restart of the bridge then, are one run at the newest"):
          host.succeed("touch /var/lib/converge/fail", "mkfifo /var/lib/converge/hold")
          held = push("held")
          host.wait_until_succeeds(f"test \"$(wc -l < {runs})\" -eq 4", timeout=60)
          host.succeed("rm /var/lib/converge/fail")
          push("passed-over")
          newest = push("newest")
          host.wait_until_succeeds(f"test -e {request} && grep -qx {newest} /run/valley-on-main-converge/commit", timeout=30)
          host.succeed("systemctl restart valley-on-main-converge.service")
          host.wait_until_succeeds(
              "journalctl -o cat"
              " _SYSTEMD_INVOCATION_ID=$(systemctl show -P InvocationID valley-on-main-converge.service)"
              f" | grep -qx 'requested a run for {newest}'",
              timeout=30,
          )
          # Opened before it is removed, so the next run does not wait.
          host.succeed("exec 3>/var/lib/converge/hold && rm /var/lib/converge/hold && echo go >&3")
          settled(5)
          assert recorded()[3:] == [f"{held} /srv/git/app.git", f"{newest} /srv/git/app.git"], recorded()
          assert show("converge.service", "Result") == "success"

      with subtest("the bridge's user has no grant over any unit"):
          bridge = "sudo -u valley-on-main-converge systemctl --no-ask-password"
          for refused in [
              f"{bridge} start converge.service",
              f"{bridge} start other.service",
              f"{bridge} stop converge.service",
              f"{bridge} restart converge.service",
              "sudo -u git systemctl --no-ask-password start converge.service",
          ]:
              out = host.fail(f"{refused} 2>&1")
              assert "Access denied" in out or "Interactive authentication required" in out, (refused, out)
          assert len(recorded()) == 5, recorded()
          host.succeed("test \"$(systemctl show -P ExecMainStartTimestampMonotonic other.service)\" = 0")

      with subtest("the bridge's user chooses the commit, and nothing else of the unit's environment"):
          commit_file = "/run/valley-on-main-converge/commit"
          environment = "/run/valley-on-main/converge.environment"
          as_bridge = "sudo -u valley-on-main-converge"
          payload = f"{newest}\\nLD_PRELOAD=/nonexistent.so\\n"
          host.succeed(
              f"printf '{payload}' | {as_bridge} tee {commit_file}.new >/dev/null",
              f"{as_bridge} mv {commit_file}.new {commit_file}",
              f"{as_bridge} touch {request}",
          )
          host.wait_until_succeeds("journalctl -u converge -o cat | grep -q 'holds something other than one commit ID'", timeout=60)
          settled(5)
          assert show("converge.service", "Result") == "exit-code"
          assert show("valley-on-main-converge.path", "Result") == "success"
          host.succeed(f"! grep -q LD_PRELOAD {environment}")
          host.fail(f"{as_bridge} touch /run/valley-on-main/planted", f"{as_bridge} rm -f {environment}")

      with subtest("a start the unit's start limit refuses stops the path unit until the operator resets both"):
          limited = "/var/lib/limited/runs"
          host.wait_until_succeeds("test \"$(systemctl show -P Result valley-on-main-limited.path)\" = unit-start-limit-hit", timeout=60)
          assert show("limited.service", "Result") == "start-limit-hit"
          host.succeed("test -e /run/valley-on-main-limited/request")
          assert host.succeed(f"cat {limited}").splitlines() == [first]
          host.succeed("systemctl reset-failed limited.service", "systemctl restart valley-on-main-limited.path")
          host.wait_until_succeeds(f"test \"$(wc -l < {limited})\" -eq 2", timeout=60)
          assert host.succeed(f"cat {limited}").splitlines() == [first, newest]
          host.wait_until_succeeds("test \"$(systemctl show -P SubState valley-on-main-limited.path)\" = waiting", timeout=30)
          host.succeed("test ! -e /run/valley-on-main-limited/request")
    '';
  };
}
