# Telemetry: the collector's unit tests, and telemetry on a host end to end.
#
# telemetry-e2e is one NixOS machine running the module with the worker,
# the controllers, the registry compiler and the collector. Every read and
# report goes over its real sshd, through the git user's shell.
#
# First the boundary: an enrolled observer reads, and nobody else does. A
# contributor without the grant, an untagged key, a key claiming another
# principal through its environment, a reporter, and every malformed or
# hostile command are refused, and none reaches a shell. A forced
# fetch-only key stays fetch-only. Reads work while pushes are held. The
# git user and the collector cannot read the worker's key, and the git
# user cannot read the diagnostics on disk.
#
# Then the worker: a pass's slow check prints its output while it builds,
# and a reader sees it then. The pass is killed during the build; the
# source reads as stale, the journal shows the kill, and the next pass
# records the interruption, inferred, and finishes. A change whose check
# fails leaves a backoff naming the change, its head, its tree and why.
#
# Then the workstation, alice, a second user reaching the host over ssh
# as its own principal. Her approval of a fresh change builds a slow and a
# fast check side by side; the observer sees the fast one finish, and the
# slow one's output, while the slow one still builds, and then the request
# published, with alice's CLI version and her own timestamps. An approval
# reusing the worker's evidence beside a local command check, and one
# reusing everything, are observable too, and so is the integrator's
# decision.
#
# Last, outage: with the collector stopped, and with the telemetry remote
# unreachable, approvals and pushes still complete, within a bounded
# overhead; once the collector is back, one flush sends what was spooled,
# and a second sends nothing again. Report and read limits hold on the
# server whatever is asked.
{
  pkgs,
  lib,
  self,
  packages,
  ...
}:
let
  keys = pkgs.runCommand "valley-telemetry-e2e-keys" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    for who in integrator contributor operator alice stranger fetcher; do
      ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$out/$who"
    done
  '';
  pub = who: lib.fileContents "${keys}/${who}.pub";
  pubBlob = who: lib.concatStringsSep " " (lib.take 2 (lib.splitString " " (pub who)));

  declaration = pkgs.writeText "valley-telemetry-e2e.cue" ''
    package valley

    projects: {
      "instance": protection: refs: ["refs/heads/main"]
      "project": protection: {}
    }
  '';

  # Every change requires a slow and a fast nix check; one that touches
  # local/ also requires a command check, which the worker leaves to
  # approve.
  floor = pkgs.writeText "floor.cue" ''
    package verification

    floor: {
    	checks: {
    		slow: {runner: "nix", attribute: "slow"}
    		fast: {runner: "nix", attribute: "fast"}
    		local: {runner: "command", command: "echo local check ran"}
    	}
    	classes: {
    		policy: {paths: {"policy/**": true}, requires: {}}
    		local: {paths: {"local/**": true}, requires: {slow: true, fast: true, local: true}}
    	}
    	unclassified: {slow: true, fast: true}
    }
  '';

  flake = pkgs.writeText "flake.nix" ''
    {
      outputs =
        { self }:
        let
          check = name: script: derivation {
            inherit name;
            system = "x86_64-linux";
            builder = "/bin/sh";
            args = [ script ];
          };
        in
        {
          checks.x86_64-linux = {
            slow = check "telemetry-slow" ./slow.sh;
            fast = check "telemetry-fast" ./fast.sh;
          };
        };
    }
  '';
  flakeLock = pkgs.writeText "flake.lock" ''{"nodes":{"root":{}},"root":"root","version":7}'';
  # About half a second a tick, a line of output each, and a terminal
  # escape in the first, which a reader must see escaped.
  slow = pkgs.writeText "slow.sh" ''
    printf 'slow start \033[31mred\033[0m\n'
    n=0
    while [ $n -lt 40 ]; do
      i=0
      while [ $i -lt 250000 ]; do i=$((i+1)); done
      n=$((n+1))
      echo "slow tick $n"
    done
    echo slow > $out
  '';
  fast = pkgs.writeText "fast.sh" "echo fast > $out";
  broken = pkgs.writeText "broken.sh" ''
    echo "broken check says why"
    exit 1
  '';
in
{
  telemetry-unit = packages.telemetry.unit;

  telemetry-e2e = pkgs.testers.runNixOSTest {
    name = "valley-telemetry-e2e";
    nodes.host =
      { lib, ... }:
      {
        imports = [ self.nixosModules.default ];
        virtualisation.cores = 4;
        virtualisation.memorySize = 3072;
        environment.systemPackages = [
          pkgs.git
          packages.valley
        ];
        users.users.alice.isNormalUser = true;
        services.valley = {
          enable = true;
          config = declaration;
          authorizedKeys = [
            {
              principal = "contributor";
              key = pub "contributor";
            }
            {
              principal = "operator";
              key = pub "operator";
            }
            {
              principal = "alice";
              key = pub "alice";
            }
            (pub "stranger")
            ''command="git-upload-pack 'project.git'" ${pub "fetcher"}''
          ];
          grants.request = [ "alice" ];
          identity.enable = true;
          buildSlice.enable = true;
          integrator = {
            enable = true;
            interval = "5s";
            signingKeyFile = "${keys}/integrator";
            instanceProject = "instance";
            signerProjects."worker/attestations" = [ "project" ];
          };
          worker = {
            enable = true;
            projects = [ "project" ];
            signerName = "worker/attestations";
          };
          telemetry = {
            enable = true;
            observers = [ "operator" ];
            reporters = [
              "alice"
              "receiver"
            ];
            remoteSources = [
              {
                name = "remote-receiver";
                description = "a web receiver on another machine";
                principal = "receiver";
              }
            ];
          };
        };
        systemd.timers.valley-worker.timerConfig.OnActiveSec = lib.mkForce "1h";
        nix.settings.experimental-features = [
          "nix-command"
          "flakes"
        ];
        nix.settings.substituters = lib.mkForce [ ];
        networking.useDHCP = false;
      };

    testScript = ''
      import json
      import shlex
      import time

      host.wait_for_unit("multi-user.target")
      host.succeed("systemctl stop valley-worker.timer valley-worker.path")

      git = "sudo -u git git"
      ssh_opts = "-o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
      commit = "git -c user.name=t -c user.email=t@example.org commit --quiet"

      def raw(who, command, extra=""):
          """The telemetry command as a session sends it, with who's key."""
          return f"ssh {ssh_opts} {extra} -i /root/keys/{who} git@localhost {shlex.quote(command)}"

      def lines(out):
          return [json.loads(l) for l in out.splitlines() if l.strip()]

      def observe(args):
          """valley telemetry as the operator, outside any checkout."""
          return lines(host.succeed(f"cd / && valley telemetry --remote git@localhost:project.git --json {args}"))

      def of(ls, kind):
          return [l for l in ls if l.get("kind") == kind]

      def records(ls, **match):
          return [l for l in of(ls, "record") if all(l.get(k) == v for k, v in match.items())]

      def source(name):
          return [s for s in of(observe("snapshot"), "source") if s["name"] == name][0]

      def as_alice(cmd, check=True):
          full = f"sudo -u alice -H env VALLEY_ATTEST_KEY=/home/alice/keys/alice VALLEY_ATTEST_NAME=alice/attestations bash -c {shlex.quote('cd /home/alice/project && ' + cmd)}"
          return host.succeed(full) if check else host.execute(full)

      def registry(worker):
          def human(who, signs):
              s = f', signs: "{who}/attestations"' if signs else ""
              return [
                  f'"{who}": {{',
                  '  kind: "human"',
                  f'  keys: [{{class: "ssh-ed25519", bound: "host", public: "{blob[who]}"{s}}}]',
                  '  grants: {push: boundary: "push", govern: boundary: "registry"}',
                  "}",
              ]
          principals = human("contributor", False) + human("alice", True)
          if worker:
              principals += [
                  '"worker": {',
                  '  kind: "machine"',
                  f'  keys: [{{class: "ssh-ed25519", bound: "host", public: "{worker}", signs: "worker/attestations"}}]',
                  '  grants: push: boundary: "push"',
                  '  expires: "2099-01-01"',
                  "}",
              ]
          return "\n".join(['package identity', 'boundaries: {"push": kind: "git-push", "registry": kind: "registry"}',
                            'genesis: "contributor"', "principals: {"] + principals + ["}"])

      blob = {"contributor": "${pubBlob "contributor"}", "alice": "${pubBlob "alice"}"}

      def land_instance(worker):
          host.succeed(
              "rm -rf /tmp/instance && mkdir -p /tmp/instance/policy/instance /tmp/instance/identity",
              "cp ${floor} /tmp/instance/policy/instance/floor.cue",
              f"cat > /tmp/instance/identity/registry.cue <<'EOF'\n{registry(worker)}\nEOF",
              f"cd /tmp/instance && git init --quiet --initial-branch=main && git add -A && {commit} -m instance",
              "chown -R git:git /tmp/instance",
              f"{git} -C /srv/git/instance.git fetch --quiet --force /tmp/instance main:refs/heads/main",
              "systemctl start valley-identity.service",
          )

      def settle():
          """Waits out a valley-init run: a unit that requires it starts it
          again, such as the identity timer's compile, and pushes pause
          while it runs."""
          host.wait_until_succeeds("test \"$(systemctl show -p ActiveState --value valley-init)\" != activating", timeout=60)

      def pushing(cmd):
          """Runs a push of alice's, again if a valley-init run paused it."""
          for attempt in range(3):
              settle()
              status, out = as_alice(cmd, check=False)
              if status == 0:
                  return out
              time.sleep(2)
          raise Exception(f"alice could not run {cmd}: {out}")

      def ready(branch, path, script=None, rebuild=False):
          """A change by alice: one new file, and the slow check's script
          given, or changed so that its derivation is built again."""
          cp = f"cp {script} slow.sh && " if script else ""
          if rebuild:
              cp += f"echo '# {branch}' >> slow.sh && "
          as_alice(
              f"git fetch --quiet origin && git checkout --quiet -B {branch} origin/main && {cp}"
              f"mkdir -p $(dirname {path}) && echo {branch} > {path} && git add -A && {commit} -m {branch}"
          )
          pushing(f"git push --quiet origin {branch} && valley ready {branch}")
          return as_alice(f"git rev-parse {branch}").strip()

      with subtest("the instance and the project are seeded, and alice has a checkout"):
          land_instance(None)
          host.succeed(
              "mkdir -p /tmp/project/policy",
              "cp ${flake} /tmp/project/flake.nix",
              "cp ${flakeLock} /tmp/project/flake.lock",
              "cp ${slow} /tmp/project/slow.sh",
              "cp ${fast} /tmp/project/fast.sh",
              "echo 'git@host:instance.git' > /tmp/project/policy/valley",
              f"cd /tmp/project && git init --quiet --initial-branch=main && git add -A && {commit} -m project",
              "chown -R git:git /tmp/project",
              f"{git} -C /srv/git/project.git fetch --quiet /tmp/project main:refs/heads/main",
              "mkdir -p /root/keys /root/.ssh && cp ${keys}/* /root/keys/ && chmod 600 /root/keys/*",
              "printf 'Host localhost host\\n  IdentityFile /root/keys/operator\\n  IdentitiesOnly yes\\n"
              "  StrictHostKeyChecking no\\n  UserKnownHostsFile /dev/null\\n' > /root/.ssh/config",
              "mkdir -p /home/alice/keys /home/alice/.ssh && install -m 600 ${keys}/alice /home/alice/keys/alice",
              "printf 'Host localhost host\\n  IdentityFile /home/alice/keys/alice\\n  IdentitiesOnly yes\\n"
              "  StrictHostKeyChecking no\\n  UserKnownHostsFile /dev/null\\n' > /home/alice/.ssh/config",
              "chown -R alice /home/alice",
          )
          host.succeed("sudo -u alice -H git clone --quiet git@localhost:project.git /home/alice/project")
          as_alice("git config valley.principal alice && git config user.name alice && git config user.email alice@example.org"
                   " && git config valley.telemetry true")
          # The worker makes its key, and is enrolled.
          host.fail("systemctl start valley-worker.service")
          land_instance(" ".join(host.succeed("cat /var/lib/valley-worker/key.pub").split()[:2]))
          host.wait_until_succeeds(
              f"{git} -C /srv/git/instance.git cat-file blob refs/the-valley/signers:known-signers | grep -q '^alice/attestations+'",
              timeout=60,
          )

      with subtest("an enrolled observer reads the catalog, and nobody else does"):
          srcs = {s["name"]: s for s in of(observe("sources"), "source")}
          for name in ["worker", "integrator", "identity", "signers", "mirrors", "convergence", "telemetry", "report.alice"]:
              assert srcs[name]["enabled"], (name, srcs[name])
          for name in ["bus", "backup", "web-publisher", "web-receiver", "restacker"]:
              assert srcs[name]["state"] == "disabled", srcs[name]
          assert srcs["remote-receiver"]["location"] == "remote" and srcs["remote-receiver"]["state"] == "unavailable", srcs["remote-receiver"]
          assert srcs["integrator"]["not_instrumented"], srcs["integrator"]
          assert srcs["worker"]["configured"]["attest"].startswith("/nix/store/"), srcs["worker"]
          props = srcs["worker"]["unit_states"][0]
          assert "Environment" not in props and props["Id"] == "valley-worker.service", props

          for who, command, said in [
              ("contributor", "valley-telemetry sources", "does not hold the telemetry observe grant"),
              ("stranger", "valley-telemetry sources", "a key that names a principal"),
              ("alice", "valley-telemetry sources", "does not hold the telemetry observe grant"),
              ("operator", "valley-telemetry report", "does not hold the telemetry report grant"),
              ("operator", "valley-telemetry sources; id", "not a telemetry operation"),
              ("operator", "valley-telemetry sources && id", "is not a flag"),
              ("operator", "valley-telemetry read --source=../../var/lib/valley-worker/key", "not a source name"),
              ("operator", "valley-telemetry read --source=valley-*", "not a source name"),
              ("operator", "valley-telemetry read --unit=sshd.service", "takes no --unit"),
              ("operator", "valley-telemetry exec id", "not a telemetry operation"),
              ("operator", "valley-telemetry read --source=nosuch", "no source is named nosuch"),
          ]:
              status, out = host.execute(raw(who, command) + " < /dev/null 2>&1")
              assert status != 0 and said in out and "uid=" not in out, (who, command, status, out)
          for command in ["id", "sh -c id", "systemctl restart valley-worker", "journalctl -u valley-worker"]:
              status, out = host.execute(raw("operator", command) + " < /dev/null 2>&1")
              assert status != 0 and "uid=" not in out and '"kind"' not in out, (command, out)
          # A principal the client names in its environment is no principal.
          status, out = host.execute(raw("contributor", "valley-telemetry sources", "-o SetEnv=VALLEY_PRINCIPAL=operator") + " 2>&1")
          assert status != 0 and '"kind": "source"' not in out and '"kind":"source"' not in out, out
          # A forced fetch-only key runs its forced command whatever it asks.
          status, out = host.execute(raw("fetcher", "valley-telemetry sources") + " < /dev/null 2>&1")
          assert '"kind"' not in out and "refs/heads/main" in out, out
          # Reading is open while pushes are held.
          host.succeed("touch /srv/git/.valley-hold")
          assert of(observe("sources"), "end")
          host.fail("sudo -u alice -H bash -c 'cd /home/alice/project && git push --quiet origin HEAD:refs/heads/held'")
          host.succeed("rm /srv/git/.valley-hold")

      with subtest("private keys stay private, and the diagnostics are the collector's alone to read"):
          for user in ["git", "valley-telemetry"]:
              host.fail(f"sudo -u {user} cat /var/lib/valley-worker/key")
              host.fail(f"sudo -u {user} ls /var/lib/valley-worker")
          host.fail("sudo -u git ls /var/lib/valley-telemetry/worker")
          host.fail("sudo -u git ls /var/lib/valley-telemetry/collector")
          assert "systemd-journal" not in host.succeed("id -nG git")
          assert host.succeed("stat -c '%a %U %G' /var/lib/valley-telemetry/worker").strip() == "2750 valley-worker valley-telemetry"
          assert host.succeed("stat -c '%a %U %G' /var/lib/valley-worker").strip() == "700 valley-worker valley-worker"
          assert host.succeed("stat -c '%a %U' /run/valley-telemetry.sock").strip() == "600 git"

      with subtest("a reader sees a worker's slow check build while it builds, and its interruption after a kill"):
          good = ready("good", "good.txt")
          host.succeed("systemctl start --no-block valley-worker.service")
          host.wait_until_succeeds(
              "valley telemetry --remote git@localhost:project.git read --source=worker --logs | grep -q 'slow tick 3'",
              timeout=180,
          )
          ls = observe("read --source=worker --limit=1000")
          starts = records(ls, type="check.build-start", check="slow")
          assert starts and not records(ls, type="check.result", check="slow"), ls
          assert starts[-1]["head"] == good and starts[-1]["tree"] and starts[-1]["change"], starts[-1]
          shown = host.succeed("valley telemetry --remote git@localhost:project.git read --source=worker --logs")
          assert "untrusted output" in shown and "\\x1b[31mred" in shown and "\x1b" not in shown, shown
          assert "\x1b" not in "".join(json.dumps(l) for l in observe("read --source=worker --logs"))
          assert not [r for r in observe("read --source=worker") if "slow tick" in json.dumps(r)]

          host.succeed("systemctl kill --signal=KILL valley-worker.service")
          host.wait_until_succeeds("test \"$(systemctl show -p ActiveState --value valley-worker)\" != activating")
          worker = source("worker")
          assert worker["state"] == "stale" and "interrupted" in worker["detail"], worker
          assert worker["unit_states"][0]["Result"] == "signal", worker
          lifecycle = of(observe("read --source=worker"), "lifecycle")
          assert any("status=9/KILL" in l["message"] for l in lifecycle), lifecycle

          host.succeed("systemctl start valley-worker.service")
          state = source("worker")["exported_state"]
          assert state["pass"] is None, state
          kinds = [(a["kind"], a["result"]) for a in state["recent"]]
          assert ("pass", "interrupted") in kinds and ("pass", "passed") in kinds and ("build", "passed") in kinds, kinds
          interrupted = [a for a in state["recent"] if a["result"] == "interrupted"][0]
          assert interrupted["inferred"] and interrupted["head"] == good, interrupted
          ls = observe("read --source=worker --limit=1000")
          assert records(ls, type="pass.interrupted") and records(ls, type="publish.end")
          results = records(ls, type="check.result")
          assert {r["check"] for r in results} >= {"slow", "fast"}, results
          assert all(r["data"]["result"] == "passed" and r["data"]["build_ms"] >= 0 for r in results[-2:]), results

      with subtest("a failed batch leaves a backoff naming the change, its head, its tree and why"):
          broken = ready("broken", "broken.txt", "${broken}")
          host.fail("systemctl start valley-worker.service")
          state = source("worker")["exported_state"]
          failure = [f for f in state["failures"] if f["head"] == broken]
          assert failure and failure[0]["tree"] and failure[0]["change"] and failure[0]["retry"], state
          assert "slow failed" in failure[0]["reason"], failure
          backoff = records(observe("read --source=worker --limit=1000"), type="backoff")
          assert backoff and backoff[-1]["head"] == broken and backoff[-1]["data"]["retry"], backoff
          assert "broken check says why" in host.succeed("valley telemetry --remote git@localhost:project.git read --source=worker --logs")
          # Records survive the collector's restart: each connection is a
          # collector of its own, and its state is on disk.
          host.succeed("systemctl restart valley-telemetry.socket")
          assert records(observe("read --source=worker --limit=1000"), type="backoff")

      with subtest("an observer watches alice's approval build a slow and a fast check side by side"):
          fresh = ready("fresh", "fresh.txt", rebuild=True)
          settle()
          host.succeed(
              "systemd-run --unit=approve-fresh --uid=alice --setenv=HOME=/home/alice"
              " --setenv=PATH=/run/wrappers/bin:/run/current-system/sw/bin"
              " --setenv=VALLEY_ATTEST_KEY=/home/alice/keys/alice --setenv=VALLEY_ATTEST_NAME=alice/attestations"
              " --working-directory=/home/alice/project --remain-after-exit"
              f" valley approve --head {fresh} fresh"
          )
          seen: list = []
          for _ in range(120):
              ls = observe("read --source=report.alice --limit=1000 --logs")
              fast_done = [r for r in records(ls, type="check.result", check="fast") if r["head"] == fresh]
              slow_done = [r for r in records(ls, type="check.result", check="slow") if r["head"] == fresh]
              slow_out = [r for r in records(ls, type="check.output", check="slow") if "slow tick" in r["data"]["text"]]
              if fast_done and slow_out and not slow_done:
                  seen = ls
                  break
              assert not slow_done, "the slow check ended before the observer saw the fast one end"
              assert host.succeed("systemctl show -p Result --value approve-fresh").strip() == "success", "the approval failed"
              time.sleep(1)
          assert seen, "never saw the fast check done while the slow one built"
          assert all(r["data"]["untrusted"] for r in records(seen, type="check.output"))
          assert not records(observe("read --source=report.alice --limit=1000"), type="check.output")
          host.wait_until_succeeds("test \"$(systemctl show -p SubState --value approve-fresh)\" = exited", timeout=300)
          assert host.succeed("systemctl show -p ExecMainStatus --value approve-fresh").strip() == "0"
          time.sleep(4)
          host.succeed("sudo -u alice -H bash -c 'cd /home/alice/project && valley telemetry flush'")
          ls = observe("read --source=report.alice --limit=1000")
          mine = [r for r in of(ls, "record") if r.get("head") == fresh]
          types = [r["type"] for r in mine]
          for want in ["approve.start", "approve.policy", "approve.coverage", "approve.checks", "run.start", "check.result", "evidence.stored", "run.end", "approve.request", "approve.end"]:
              assert want in types, (want, types)
          start = [r for r in mine if r["type"] == "approve.start"][0]
          assert start["principal"] == "alice" and start["received"] and start["producer"]["version"] and start["producer"]["exe"].startswith("/nix/store/"), start
          cover = [r for r in mine if r["type"] == "approve.coverage"][0]["data"]
          assert cover["reused"] == [] and sorted(cover["missing"]) == ["fast", "slow"], cover
          slow_r = [r for r in mine if r["type"] == "check.result" and r["check"] == "slow"][0]["data"]
          fast_r = [r for r in mine if r["type"] == "check.result" and r["check"] == "fast"][0]["data"]
          assert fast_r["build_ended"] < slow_r["build_ended"] and fast_r["build_ms"] < slow_r["build_ms"], (fast_r, slow_r)
          assert [r for r in mine if r["type"] == "approve.request"][0]["data"]["result"] == "pushed"
          assert [r for r in mine if r["type"] == "approve.end"][0]["data"]["result"] == "filed"

      with subtest("an observer watches an approval made from review, after a refused attempt in the same session"):
          reviewed = ready("reviewed", "reviewed.txt", rebuild=True)
          # One review session answers a, is refused for want of a key,
          # and answers a again once the key is there. Its answers come
          # through a FIFO a sleeping writer holds open between them.
          review = shlex.quote(
              "sudo -u alice -H env VALLEY_ATTEST_KEY=/home/alice/keys/later VALLEY_ATTEST_NAME=alice/attestations"
              " bash -c 'cd /home/alice/project && exec valley review reviewed'"
              " < /tmp/review.fifo > /tmp/review.out 2>&1"
          )
          host.succeed("rm -f /tmp/review.fifo /home/alice/keys/later && mkfifo -m 600 /tmp/review.fifo")
          settle()
          host.succeed(f"setsid -f sh -c {review} < /dev/null > /dev/null 2>&1")
          host.succeed("setsid -f sh -c 'echo $$ > /tmp/review.holder; exec sleep 900 > /tmp/review.fifo' < /dev/null > /dev/null 2>&1")
          host.wait_until_succeeds("grep -q 'pprove' /tmp/review.out", timeout=120)
          host.succeed("echo a > /tmp/review.fifo")
          refused: list = []
          for _ in range(60):
              ls = observe("read --source=report.alice --limit=1000")
              refused = [r for r in records(ls, type="approve.end") if r["head"] == reviewed and r["data"]["result"] == "refused"]
              if refused:
                  break
              time.sleep(1)
          assert refused, "the refused approval from review was never observed"
          first = refused[0]["run"]
          first_types = [r["type"] for r in of(ls, "record") if r.get("run") == first]
          assert first_types[:3] == ["approve.start", "approve.policy", "approve.coverage"] and "approve.checks" not in first_types, first_types

          host.succeed("install -m 600 -o alice ${keys}/alice /home/alice/keys/later")
          host.wait_until_succeeds("test $(grep -o 'pprove' /tmp/review.out | wc -l) -ge 2", timeout=60)
          host.succeed("echo a > /tmp/review.fifo")
          live: list = []
          for _ in range(120):
              ls = observe("read --source=report.alice --limit=1000 --logs")
              fast_done = [r for r in records(ls, type="check.result", check="fast") if r["head"] == reviewed]
              if fast_done:
                  checks = fast_done[0]["run"]
                  slow_done = [r for r in records(ls, type="check.result", check="slow") if r["run"] == checks]
                  slow_out = [r for r in records(ls, type="check.output", check="slow") if r["run"] == checks and "slow tick" in r["data"]["text"]]
                  assert not slow_done, "the slow check ended before the observer saw the fast one end"
                  if slow_out:
                      live = ls
                      break
              time.sleep(1)
          assert live, "never saw the fast check done while the slow one built, in an approval from review"
          host.wait_until_succeeds("grep -q '^Approved ' /tmp/review.out", timeout=300)
          host.succeed("kill $(cat /tmp/review.holder)")
          time.sleep(4)
          as_alice("valley telemetry flush")
          ls = observe("read --source=report.alice --limit=1000")
          second = [r for r in records(ls, type="approve.end") if r["head"] == reviewed and r["data"]["result"] == "filed"]
          assert len(second) == 1 and second[0]["run"] != first, second
          run = second[0]["run"]
          types = [r["type"] for r in of(ls, "record") if r.get("run") == run]
          for want in ["approve.start", "approve.policy", "approve.coverage", "approve.checks", "approve.request", "approve.end"]:
              assert want in types, (want, types)
          assert [r for r in records(ls, type="approve.request") if r["run"] == run][0]["data"]["result"] == "pushed"
          slow = [r for r in records(ls, type="check.result", check="slow") if r["run"] == run + "-checks"]
          assert slow and slow[0]["data"]["result"] == "passed", slow
          assert len([r for r in records(ls, type="approve.end") if r["run"] == first]) == 1

      with subtest("an approval reusing the worker's evidence beside a local check, and one reusing everything, are observable"):
          mixed = ready("mixed", "local/mixed.txt")
          host.succeed("systemctl start valley-worker.service")
          pushing(f"valley approve --head {mixed} mixed")
          pushing(f"valley approve --head {good} good")
          as_alice("valley telemetry flush")
          ls = observe("read --source=report.alice --limit=1000")
          cover = [r for r in records(ls, type="approve.coverage") if r["head"] == mixed][0]["data"]
          assert sorted(cover["reused"]) == ["fast", "slow"] and cover["missing"] == ["local"], cover
          assert len(cover["reused_by"]) == 2 and all("worker" in s for s in cover["reused_by"]), cover
          local = [r for r in records(ls, type="check.result", check="local") if r["head"] == mixed]
          assert local and local[0]["data"]["result"] == "passed", local
          cover = [r for r in records(ls, type="approve.coverage") if r["head"] == good][0]["data"]
          assert sorted(cover["reused"]) == ["fast", "slow"] and cover["missing"] == [], cover
          good_types = [r["type"] for r in of(ls, "record") if r.get("head") == good]
          assert "approve.checks" not in good_types and "approve.end" in good_types, good_types
          host.wait_until_succeeds(
              "valley telemetry --remote git@localhost:project.git --json read --source=integrator | grep -q '\"type\":\"decision\"'",
              timeout=120,
          )
          decision = records(observe("read --source=integrator"), type="decision")[-1]
          assert decision["project"] and decision["data"]["outcome"] and "duration_ms" in decision["data"], decision

      with subtest("approval and pushes go on when telemetry cannot be reached, and each run's records go only where its run began"):
          def timed_approve(branch, path):
              head = ready(branch, path)
              settle()
              began = time.monotonic()
              as_alice(f"valley approve --head {head} {branch}")
              return time.monotonic() - began, head
          as_alice("git config valley.telemetry false")
          plain, _ = timed_approve("plain", "policy/plain.txt")
          as_alice("git config valley.telemetry true")
          host.succeed("systemctl stop valley-telemetry.socket")
          down, head_down = timed_approve("down", "policy/down.txt")
          as_alice("git config valley.telemetry.remote ssh://git@192.0.2.1/project.git")
          unreachable, head_away = timed_approve("away", "policy/away.txt")
          as_alice("git config --unset valley.telemetry.remote")
          assert down < plain + 5 and unreachable < plain + 5, (plain, down, unreachable)
          pushing("git push --quiet origin HEAD:refs/heads/still-pushes")
          host.fail("sudo -u alice -H bash -c 'cd /home/alice/project && valley telemetry --remote origin flush'")
          host.succeed("systemctl start valley-telemetry.socket")
          time.sleep(12)
          as_alice("valley telemetry --remote origin flush")
          assert "0 records sent to ssh://git@localhost" in as_alice("valley telemetry --remote origin flush 2>&1")
          # The run begun while the telemetry remote named 192.0.2.1 stays
          # bound to it: a flush from the same checkout, back on origin,
          # tries that destination and sends its records nowhere else.
          status, out = as_alice("valley telemetry flush 2>&1", check=False)
          assert status != 0 and "ssh://git@192.0.2.1" in out, out
          ls = observe("read --source=report.alice --limit=1000")
          ends = [r for r in records(ls, type="approve.end") if r["head"] == head_down]
          assert len(ends) == 1 and ends[0]["data"]["result"] == "filed", ends
          assert not [r for r in of(ls, "record") if r.get("head") == head_away], "a run bound to another destination reached this one"

      with subtest("the server holds every request to its limits"):
          big = '{"type":"approve.start","time":"2026-01-01T00:00:00.000Z","client_id":"0123456789abcdef:1","data":{"x":"' + "y" * 20000 + '"}}'
          host.succeed(f"echo '{big}' > /tmp/big.json")
          answer = lines(host.succeed(raw("alice", "valley-telemetry report") + " < /tmp/big.json"))
          assert of(answer, "ack")[0]["rejected"] == 1 and of(answer, "ack")[0]["accepted"] == 0, answer
          host.execute("timeout 5 " + raw("operator", "valley-telemetry follow --seconds=99999 --source=telemetry") + " < /dev/null > /tmp/follow.json")
          header = of(lines(host.succeed("head -1 /tmp/follow.json")), "header")[0]
          assert header["limits"]["follow_seconds"] == 300, header
          header = of(observe("read --limit=999999"), "header")[0]
          assert header["limits"]["read_limit"] == 1000, header
    '';
  };
}
