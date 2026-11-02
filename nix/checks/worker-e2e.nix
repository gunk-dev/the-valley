# The check worker on a host: one NixOS machine running the module with the
# build slice and the worker on, beside the controllers and the registry
# compiler, and a project with ready changes. The worker makes its key the
# first time it starts, readable by it alone, and says its public halves.
# An earlier start that was interrupted leaves nothing that stands in the
# way. Until the registry holds the key, the worker cannot reach the
# host's repositories. Once it does, a pass builds the nix checks a ready
# change requires side by side, as many at once as the host gives it
# slots, through the machine's nix daemon, and the evidence is on origin
# before anyone approves the change. The timer runs the next pass.
#
# Then the wake directory. A move the worker reads starts a pass with the
# timer stopped, and a move during a pass gets the next one. A landing by
# the integrator's own service marks the worker while the project's mirror
# and the bus hang, and so does a ready push. A floor landed in the
# instance, which the worker does not build, marks it, and the next pass
# reads the new floor. With the timer at its minute, a
# pass killed during a build, and a pass that cannot reach the
# repositories, leave their claim, and the timer's next pass builds the
# change with no new mark. A failed batch is not built again before its
# wait, however many marks and timer passes come.
#
# Then the slice's caps, with builds that are not the worker's. A build
# that wants six CPUs is held to the slice's quota. A change whose check eats memory without
# end is built while two unrelated builds hold memory of their own. The
# kernel kills the process in the slice using the most memory, so the
# unrelated build bigger than the runaway is killed with it, the smaller
# one finishes, and the rest of the host goes on as it was. Last, the key
# is the same after many starts and a reboot.
#
# The slice's default caps are 4 CPUs and 8 GiB. The machine here has
# 2 GiB, so the memory cap is lowered at runtime, after the check reads
# the default values off the slice, to let builds run past it. module-eval
# holds the values the module renders.
{
  pkgs,
  lib,
  self,
  machineValley,
  ...
}:
let
  # Throwaway keys, made once at build time. They hold no authority
  # anywhere: the machine that trusts them exists for one test run. The
  # worker's key is not among them: the worker makes its own on the host.
  keys = pkgs.runCommand "valley-worker-e2e-keys" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    for who in integrator contributor; do
      ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$out/$who"
    done
  '';
  pub = who: lib.fileContents "${keys}/${who}.pub";

  declaration = pkgs.writeText "valley-worker-e2e.cue" ''
    package valley

    projects: {
      // The instance repository, which carries the floor and the registry.
      "instance": protection: refs: ["refs/heads/main"]
      // The project's mirror is a local repository, which a scenario
      // below makes hang.
      "project": {
        protection: {}
        mirrors: ["/srv/hung.git"]
      }
    }
  '';

  # A floor that requires two nix checks of every change but one to the
  # policy alone, so the instance's own floor lands by integration with no
  # evidence. floorThird, which a scenario lands in the instance, requires a
  # third check of the same attribute as the second.
  floorWith = third: pkgs.writeText "floor.cue" ''
    package verification

    floor: {
    	checks: {
    		first: {runner: "nix", attribute: "first"}
    		second: {runner: "nix", attribute: "second"}
    		${lib.optionalString third ''third: {runner: "nix", attribute: "second"}''}
    	}
    	classes: policy: {paths: {"policy/**": true}, requires: {}}
    	unclassified: {first: true, second: true${lib.optionalString third ", third: true"}}
    }
  '';
  floor = floorWith false;
  floorThird = floorWith true;

  # The project's flake, with no inputs, so the machine builds it offline.
  # Each check runs a script from the project's tree in the sandbox's
  # shell, and the script is the derivation's one input. The project's own
  # scripts spin for about half a second, several of the sampler's ticks,
  # so the two builds a pass runs at once are seen at once, and write the
  # cores the daemon gave them. The runaway change's first script instead
  # doubles a string until something stops it.
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
            first = check "worker-first" ./first.sh;
            second = check "worker-second" ./second.sh;
          };
        };
    }
  '';
  flakeLock = pkgs.writeText "flake.lock" ''{"nodes":{"root":{}},"root":"root","version":7}'';
  spin = pkgs.writeText "spin.sh" ''
    i=0
    while [ $i -lt 250000 ]; do i=$((i+1)); done
    echo $NIX_BUILD_CORES > $out
  '';
  # A build long enough to be killed in: about twenty seconds of spinning.
  slow = pkgs.writeText "slow.sh" ''
    i=0
    while [ $i -lt 10000000 ]; do i=$((i+1)); done
    echo $NIX_BUILD_CORES > $out
  '';
  broken = pkgs.writeText "broken.sh" "exit 1";
  runaway = pkgs.writeText "runaway.sh" ''
    x=a
    while :; do x=$x$x; done
  '';

  # Builds that are not the worker's, through the same daemon. small and
  # big each hold a string of 1 MiB or 128 MiB. Then each waits without
  # using a CPU, so it takes none from the worker's pass: it reads its own
  # standard output, where no input ever arrives. big waits until it is
  # stopped. small waits until it is sent SIGUSR1, and then finishes, so
  # the test decides when it ends. cpu spins on six CPUs until it is
  # stopped.
  unrelated = pkgs.writeText "unrelated.nix" ''
    { kind }:
    let
      hold = n: "x=a; n=0; while [ $n -lt ''${n} ]; do x=$x$x; n=$((n+1)); done";
      idle = "read y < /proc/self/fd/1";
      scripts = {
        small = hold "20" + "; trap 'echo ok > $out; exit 0' USR1; ''${idle}";
        big = hold "27" + "; ''${idle}";
        cpu = "for i in 1 2 3 4 5 6; do (while :; do :; done) & done; wait";
      };
    in
    derivation {
      name = "unrelated-''${kind}";
      system = "x86_64-linux";
      builder = "/bin/sh";
      args = [ "-c" scripts.''${kind} ];
    }
  '';

  # What the daemon's builds look like while the worker runs: the number
  # of build users with a process, and the cgroup of each such process,
  # sampled ten times a second.
  sampler = pkgs.writeShellScript "valley-worker-e2e-sampler" ''
    PATH=${
      lib.makeBinPath [
        pkgs.coreutils
        pkgs.gawk
        pkgs.procps
      ]
    }
    while :; do
      ps -eo user:32=,cgroup:512= | awk '$1 ~ /^nixbld/' > /tmp/sample
      awk '{print $1}' /tmp/sample | sort -u | wc -l >> /tmp/builders
      awk '{print $2}' /tmp/sample >> /tmp/build-cgroups
      sleep 0.1
    done
  '';
in
{
  worker-e2e = pkgs.testers.runNixOSTest {
    name = "valley-worker-e2e";
    nodes.host = {
      imports = [ self.nixosModules.default ];
      # Six CPUs: more than the worker's slots, so the slots and not the
      # CPU count decide how many checks build at once, and a build can
      # want more CPUs than the slice's quota of four.
      virtualisation.cores = 6;
      virtualisation.memorySize = 2048;
      environment.systemPackages = [
        pkgs.git
        machineValley
      ];
      services.valley = {
        enable = true;
        config = declaration;
        buildSlice.enable = true;
        authorizedKeys = [
          {
            principal = "contributor";
            key = pub "contributor";
          }
        ];
        identity.enable = true;
        bus.enable = true;
        integrator = {
          enable = true;
          signingKeyFile = "${keys}/integrator";
          instanceProject = "instance";
          signerProjects."worker/attestations" = [ "project" ];
        };
        # Two checks at once, each told it may use two cores, rather
        # than the defaults of one check at a time on four.
        worker = {
          enable = true;
          projects = [ "project" ];
          signerName = "worker/attestations";
          buildSlots = 2;
          maxJobs = 2;
          cores = 2;
        };
      };
      # NixOS test machines panic on any OOM, a cgroup's included
      # (test-instrumentation.nix). A host does not, and the runaway build
      # below is stopped by the slice's OOM, so a host's default is restored.
      boot.kernel.sysctl."vm.panic_on_oom" = 0;
      # The test starts each pass itself until it checks the timer, so no
      # pass runs before the test is ready for it. The interval stays the
      # module's minute: the recovery scenarios wait for it. A timer pass
      # starts within a second of it, rather than up to a minute of
      # systemd's accuracy later.
      systemd.timers.valley-worker.timerConfig.OnActiveSec = lib.mkForce "1h";
      systemd.timers.valley-worker.timerConfig.AccuracySec = "1s";
      # The machine has no network. Without these, every new build waits
      # for the daemon's lookups in cache.nixos.org to fail, and every boot
      # waits for a DHCP lease on an interface nothing here uses.
      nix.settings.substituters = lib.mkForce [ ];
      networking.useDHCP = false;
    };

    testScript = ''
      import shlex

      host.wait_for_unit("multi-user.target")
      host.succeed("systemctl is-active valley-worker.timer valley-worker.path")
      host.succeed("systemctl stop valley-worker.timer valley-worker.path")

      git = "sudo -u git git"
      wake = "/var/lib/valley-worker-wake"
      state = "/var/lib/valley-worker"
      ssh = shlex.quote(
          "ssh -i /root/keys/contributor -o IdentitiesOnly=yes -o BatchMode=yes"
          " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
      )
      contributor = f"cd /root/project && GIT_SSH_COMMAND={ssh}"
      commit = "git -c user.name=contributor -c user.email=contributor@localhost commit --quiet"

      def journal():
          return host.succeed("journalctl -u valley-worker -o cat")

      def unrelated(kind):
          host.succeed(
              f"systemd-run --unit=unrelated-{kind} --remain-after-exit --setenv=NIX_REMOTE=daemon"
              f" nix-build ${unrelated} --argstr kind {kind} --no-out-link"
          )

      def unit_property(name, prop):
          return host.succeed(f"systemctl show -p {prop} --value {name}").strip()

      def attestations():
          return host.succeed(
              f"{git} -C /srv/git/project.git for-each-ref --format='%(refname)' refs/the-valley/attestations/"
          ).split()

      def registry(worker):
          principals = [
              '"contributor": {',
              '  kind: "human"',
              '  keys: [{class: "ssh-ed25519", bound: "host", public: "${
                lib.concatStringsSep " " (lib.take 2 (lib.splitString " " (pub "contributor")))
              }"}]',
              '  grants: {push: boundary: "push", govern: boundary: "registry"}',
              "}",
          ]
          if worker:
              principals += [
                  '"worker": {',
                  '  kind: "machine"',
                  f'  keys: [{{class: "ssh-ed25519", bound: "host", public: "{worker}", signs: "worker/attestations"}}]',
                  '  grants: push: boundary: "push"',
                  '  expires: "2099-01-01"',
                  "}",
              ]
          return "\n".join(
              [
                  "package identity",
                  'boundaries: {"push": kind: "git-push", "registry": kind: "registry"}',
                  'genesis: "contributor"',
                  "principals: {",
              ]
              + principals
              + ["}"]
          )

      # The instance's main carries the floor and the registry. It is
      # landed by a local write, which runs no hook, as an operator seeds
      # a protected repository.
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

      # A branch of the project with the first check's script given,
      # pushed by the contributor and readied.
      def ready(branch, first):
          host.succeed(
              f"cd /root/project && git checkout --quiet -b {branch} origin/main",
              f"cp {first} /root/project/first.sh && echo {branch} >> /root/project/README.md",
              f"cd /root/project && git add -A && {commit} -m {branch}",
              f"{contributor} git push --quiet origin {branch}",
              f"{contributor} valley ready {branch}",
          )

      with subtest("the instance and the project are seeded, and a change is ready"):
          host.succeed("mkdir /srv/hung.git && chown git:git /srv/hung.git", f"{git} init --quiet --bare /srv/hung.git")
          land_instance(None)
          host.succeed(
              "mkdir -p /tmp/project/policy",
              "cp ${flake} /tmp/project/flake.nix",
              "cp ${flakeLock} /tmp/project/flake.lock",
              "cp ${spin} /tmp/project/first.sh",
              "cp ${spin} /tmp/project/second.sh",
              "echo 'git@host:instance.git' > /tmp/project/policy/valley",
              "echo project > /tmp/project/README.md",
              f"cd /tmp/project && git init --quiet --initial-branch=main && git add -A && {commit} -m project",
              "chown -R git:git /tmp/project",
              f"{git} -C /srv/git/project.git fetch --quiet /tmp/project main:refs/heads/main",
              "mkdir -p /root/keys && install -m 600 ${keys}/contributor /root/keys/contributor",
              f"GIT_SSH_COMMAND={ssh} git clone --quiet git@localhost:project.git /root/project",
              "git -C /root/project config valley.principal contributor",
              "git -C /root/project config user.name contributor",
              "git -C /root/project config user.email contributor@localhost",
          )
          ready("good", "${spin}")

      with subtest("the first start makes the key, readable by the worker alone, and says its public halves"):
          host.fail(f"test -e {state}/key")
          # What an earlier start killed half way leaves: a partial key and
          # public half under the temporary names, and a public half with
          # no key.
          host.succeed(
              f"sudo -u valley-worker sh -c 'echo partial > {state}/key.new;"
              f" echo partial > {state}/key.new.pub; echo stale > {state}/key.pub'"
          )
          # Not enrolled yet, so the host's sshd turns the worker away and
          # the pass stops before it builds anything.
          host.fail("systemctl start valley-worker.service")
          assert host.succeed(f"stat -c '%a %U %G' {state}").strip() == "700 valley-worker valley-worker"
          assert host.succeed(f"stat -c '%a %U %G' {state}/key").strip() == "400 valley-worker valley-worker"
          host.succeed(f"test -f {state}/key && test ! -L {state}/key")
          for other in ["git", "valley-integrator", "nobody"]:
              host.fail(f"sudo -u {other} cat {state}/key")
          public = host.succeed(f"cat {state}/key.pub").strip()
          verifier = host.succeed(f"cat {state}/verifier").strip()
          assert verifier.startswith("worker/attestations+"), verifier
          keyhash = verifier.split("+")[1]
          said = journal()
          assert f"the worker's ssh public key is {public}" in said, said
          assert f"the worker's verifier line is {verifier}" in said, said
          assert "PRIVATE KEY" not in said, said
          assert host.succeed(f"ssh-keygen -y -f {state}/key").split()[1] == public.split()[1]
          host.fail(f"ls {state} | grep -q '^key.new'")
          fingerprint = host.succeed(f"ssh-keygen -lf {state}/key").strip()
          assert attestations() == []

      with subtest("enrolled in the registry, the worker builds the ready change's checks side by side and publishes them"):
          land_instance(" ".join(public.split()[:2]))
          host.succeed(f"grep -q '^worker/attestations+{keyhash}+' /var/lib/valley-identity/known-signers")
          host.wait_until_succeeds(
              f"{git} -C /srv/git/instance.git cat-file blob refs/the-valley/signers:known-signers"
              f" | grep -q '^worker/attestations+{keyhash}+'",
              timeout=60,
          )
          host.succeed("systemd-run --unit=valley-worker-e2e-sampler ${sampler}")
          host.succeed("systemctl start valley-worker.service")
          host.succeed("systemctl stop valley-worker-e2e-sampler")
          said = journal()
          assert "building first, second" in said, said
          refs = attestations()
          assert len(refs) == 1 and refs[0].endswith(f"/{keyhash}"), refs
          assert f"published {refs[0]}" in said, said
          for check in ["first", "second"]:
              host.succeed(f"{git} -C /srv/git/project.git cat-file -e {refs[0]}:{check}/statement.note")
          # Both checks at once, each in the daemon's cgroup, inside the
          # slice, each told it may use the 2 cores the host configures.
          builders = [int(n) for n in host.succeed("cat /tmp/builders").split()]
          assert max(builders) >= 2, builders
          cgroups = set(host.succeed("cat /tmp/build-cgroups").split())
          assert cgroups and all(c.startswith("0::/valley.slice/valley-builds.slice/nix-daemon.service") for c in cgroups), cgroups
          for check in ["first", "second"]:
              assert host.succeed(f"cat /nix/store/*-worker-{check}").strip() == "2", check
          for unit in ["valley-worker.service", "nix-daemon.service"]:
              assert host.succeed(f"systemctl show -p Slice --value {unit}").strip() == "valley-builds.slice", unit

      with subtest("the timer runs the next pass, which finds the evidence on origin and builds nothing"):
          host.succeed("systemctl start valley-worker.timer")
          host.wait_until_succeeds(
              "journalctl -u valley-worker -o cat | grep -q 'origin holds evidence for every nix check it requires'",
              timeout=150,
          )
          host.succeed("systemctl stop valley-worker.timer")
          host.wait_until_succeeds("test \"$(systemctl show -p ActiveState --value valley-worker)\" != activating")
          assert len(attestations()) == 1

      def idle():
          host.wait_until_succeeds(
              "test \"$(systemctl show -p ActiveState --value valley-worker)\" != activating"
              f" && test ! -e {wake}/due",
              timeout=300,
          )

      def started():
          return unit_property("valley-worker.service", "ExecMainStartTimestampMonotonic")

      with subtest("the wake directory is the worker's and its writers', and the worker's access is unchanged"):
          assert host.succeed(f"stat -c '%U %G %a' {wake}").strip() == "valley-worker valley-worker-wake 770"
          assert "valley-worker-wake" in host.succeed("id -nG git").split()
          assert "valley-worker-wake" in host.succeed(f"id -nG {unit_property('valley-integrator@project.service', 'User')}").split()
          assert host.succeed("id -nG valley-worker").split() == ["valley-worker"]
          host.fail("sudo -u valley-worker ls /srv/git/project.git/refs")
          host.succeed("systemctl cat valley-worker.path | grep -qx 'PathExists=/var/lib/valley-worker-wake/due'")
          assert unit_property("valley-worker.service", "StartLimitIntervalUSec") == "0"
          assert unit_property("valley-worker.service", "RestartForceExitStatus") == "75"
          host.fail("systemctl show -p WantedBy --value valley-worker.service | grep -q multi-user")
          # The marks the seeding pushes and the signer publication left
          # are read by one pass, which builds nothing.
          host.succeed("systemctl start valley-worker.path")
          idle()
          host.succeed(f"test ! -e {wake}/claimed")
          assert len(attestations()) == 1

      with subtest("a ready push starts a pass at once with no timer, and a change readied during it gets the next pass"):
          import time
          host.fail("systemctl is-active valley-worker.timer")
          building = journal().count("building first, second")
          evidence = len(attestations())
          ready("wake-first", "${spin}")
          readied = time.monotonic()
          host.wait_until_succeeds(
              f"test $(journalctl -u valley-worker -o cat | grep -c 'building first, second') -gt {building}",
              timeout=120,
          )
          print(f"ready to building: {time.monotonic() - readied:.1f}s")
          assert unit_property("valley-worker.service", "ActiveState") == "activating"
          ready("wake-second", "${spin}")
          host.wait_until_succeeds(f"test -e {wake}/due -a -e {wake}/claimed")
          host.wait_until_succeeds(
              f"test $({git} -C /srv/git/project.git for-each-ref refs/the-valley/attestations/ | wc -l) -eq {evidence + 2}",
              timeout=600,
          )
          idle()
          host.fail("systemctl is-active valley-worker.timer")
          assert journal().count("building first, second") == building + 2

      with subtest("marks from the git user and the integrator's user are one file, and a stale mark builds nothing"):
          host.succeed("systemctl stop valley-worker.path")
          integrator = unit_property("valley-integrator@project.service", "User")
          for who in ["git", integrator, "git", integrator]:
              host.succeed(f"sudo -u {who} bash -c 'set -C; : > {wake}/due' 2>/dev/null || test -e {wake}/due")
          assert host.succeed(f"ls -A {wake}").split() == ["due", "lock"]
          host.succeed(f"rm {wake}/due && sudo -u {integrator} bash -c 'set -C; : > {wake}/due'")
          building = journal().count("building first, second")
          pushes = journal().count("published ")
          host.succeed("systemctl start valley-worker.path")
          idle()
          assert host.succeed(f"ls -A {wake}").split() == ["lock"]
          assert journal().count("building first, second") == building
          assert journal().count("published ") == pushes

      with subtest("a claim a killed pass left starts nothing, and the next pass acknowledges it"):
          host.succeed(f"sudo -u valley-worker touch {wake}/claimed")
          mark = started()
          host.succeed("sleep 5")
          assert started() == mark
          host.succeed("systemctl start valley-worker.service")
          host.succeed(f"test ! -e {wake}/claimed")

      with subtest("a pass that cannot claim due reads all the same and is started again only after its hold-off"):
          restarts = int(unit_property("valley-worker.service", "NRestarts"))
          host.succeed(f"chmod 0550 {wake} && touch {wake}/due")
          host.wait_until_succeeds("journalctl -u valley-worker -o cat | grep -q 'could not claim'", timeout=60)
          host.wait_until_succeeds("test \"$(systemctl show -p SubState --value valley-worker)\" = auto-restart")
          # due is still there, and nothing starts a pass for a minute.
          mark = started()
          host.sleep(20)
          assert started() == mark
          assert int(unit_property("valley-worker.service", "NRestarts")) == restarts
          host.fail("systemctl is-failed valley-worker.path")
          assert "origin holds evidence for every nix check it requires" in journal()
          host.succeed(f"chmod 0770 {wake}")
          idle()
          host.succeed("systemctl is-active valley-worker.path")

      with subtest("a push to the instance, which the worker does not build, marks it due, and knots and bookkeeping do not"):
          host.succeed("systemctl stop valley-worker.path")
          host.succeed(
              f"cd /root && rm -rf instance && GIT_SSH_COMMAND={ssh} git clone --quiet git@localhost:instance.git instance",
              f"cd /root/instance && git checkout --quiet -b floor-probe && echo probe > probe && git add probe && {commit} -m probe",
          )
          hook = "/srv/git/instance.git/hooks/post-receive.d/valley-wake"
          zero = "0" * 40
          host.succeed(f"echo '{zero} {zero} refs/the-valley/knots' | sudo -u git {hook}")
          host.succeed(f"echo '{zero} {zero} refs/the-valley/integration-outcomes/main/x' | sudo -u git {hook}")
          host.succeed(f"test ! -e {wake}/due")
          host.succeed(f"cd /root/instance && GIT_SSH_COMMAND={ssh} git push --quiet origin floor-probe")
          host.succeed(f"test -e {wake}/due")

      with subtest("a signer publication marks the worker due only when it moves the ref"):
          host.succeed(f"rm -f {wake}/due")
          host.succeed("systemctl start valley-signers.service")
          host.succeed(f"test ! -e {wake}/due")
          host.succeed(f"{git} -C /srv/git/instance.git update-ref -d refs/the-valley/signers")
          host.succeed("systemctl start valley-signers.service")
          host.succeed(f"test -e {wake}/due")
          host.succeed("systemctl start valley-worker.path")
          idle()

      def head(branch):
          return host.succeed(f"git -C /root/project rev-parse --short=7 {branch}").strip()

      def told(commit, what):
          return journal().count(f" at {commit}: {what}")

      def wait_said(commit, what, timeout):
          host.wait_until_succeeds(f"journalctl -u valley-worker -o cat | grep -qF ' at {commit}: {what}'", timeout=timeout)

      with subtest("with the mirror and the bus hung, a landing by the integrator's service and a ready push mark the worker at once"):
          # The mirror takes the drain's push of main and never answers it,
          # and what listens at the bus's address takes a connection and
          # never answers it.
          host.succeed(
              "printf '#!/bin/sh\\nexec /run/current-system/sw/bin/sleep 100001\\n' > /srv/hung.git/hooks/pre-receive",
              "chmod 755 /srv/hung.git/hooks/pre-receive",
              "systemctl stop valley-bus.service",
              "systemd-run --unit=hung-bus ${pkgs.socat}/bin/socat TCP-LISTEN:4222,bind=127.0.0.1,fork,reuseaddr"
              " SYSTEM:'exec ${pkgs.coreutils}/bin/sleep 100000'",
          )
          host.wait_for_open_port(4222)
          # The good change lands by the integrator's own service, which
          # marks due from inside its sandbox. The request ref is written
          # locally, which runs no hook, so the landing's mark is the only one.
          host.succeed("systemctl stop valley-worker.path")
          base = host.succeed(f"{git} -C /srv/git/project.git rev-parse refs/heads/main").strip()
          good = host.succeed("git -C /root/project rev-parse good").strip()
          host.succeed(f"{git} -C /srv/git/project.git update-ref refs/the-valley/integration-requests/main/good {good}")
          host.wait_until_succeeds(
              f"test \"$({git} -C /srv/git/project.git rev-parse refs/heads/main^)\" = {base}", timeout=120
          )
          landed = time.monotonic()
          host.wait_until_succeeds(f"test -e {wake}/due", timeout=10)
          print(f"landing to mark, mirror and bus hung: {time.monotonic() - landed:.1f}s")
          host.wait_until_succeeds(
              "journalctl -u valley-integrator@project -o cat | grep -q 'bus      publish of .* FAILED'", timeout=120
          )
          said_by = host.succeed("journalctl -u valley-integrator@project -o cat")
          assert said_by.index("wake     the check worker is due") < said_by.index("bus      publish of"), said_by
          host.wait_until_succeeds("pgrep -f 'sleep 10000[1]'", timeout=60)
          host.succeed("systemctl start valley-worker.path")
          idle()
          # With the mirror's push still hung, a ready push starts a pass.
          ready("hung", "${spin}")
          readied = time.monotonic()
          wait_said(head("hung"), "building", 120)
          print(f"ready to building, mirror and bus hung: {time.monotonic() - readied:.1f}s")
          wait_said(head("hung"), "published", 120)
          idle()
          host.succeed("pgrep -f 'sleep 10000[1]'")
          host.succeed(
              "rm /srv/hung.git/hooks/pre-receive && pkill -f 'sleep 10000[1]'",
              "systemctl stop hung-bus && systemctl start valley-bus.service",
          )

      with subtest("a floor landed in the instance, which the worker does not build, marks it, and the next pass reads the floor it landed"):
          host.succeed("systemctl stop valley-worker.path")
          floor_base = host.succeed(f"{git} -C /srv/git/instance.git rev-parse refs/heads/main").strip()
          host.succeed(
              "cp ${floorThird} /tmp/instance/policy/instance/floor.cue",
              f"cd /tmp/instance && sudo -u git {commit} -am third",
              f"{git} -C /srv/git/instance.git fetch --quiet /tmp/instance main:refs/the-valley/integration-requests/main/third",
          )
          host.wait_until_succeeds(
              f"test \"$({git} -C /srv/git/instance.git rev-parse refs/heads/main^)\" = {floor_base}", timeout=120
          )
          host.wait_until_succeeds(f"test -e {wake}/due", timeout=10)
          host.succeed("journalctl -u valley-integrator@instance -o cat | grep -q 'wake     the check worker is due'")
          # The pass reads the new floor. A change built before it holds this
          # worker's evidence for its tree, which admits no addition, so the
          # pass says the third check is not covered.
          host.succeed("systemctl start valley-worker.path")
          host.wait_until_succeeds(
              "journalctl -u valley-worker -o cat | grep -q 'and it does not cover third'", timeout=120
          )
          idle()
          assert "instance:" not in journal()

      with subtest("a pass killed during a build leaves its claim, and the timer's next pass builds the change"):
          # The push and the ready mark are made before the pass starts,
          # so they are one due, which the pass claims.
          host.succeed("systemctl stop valley-worker.path")
          ready("killed", "${slow}")
          killed = head("killed")
          host.succeed("systemctl start valley-worker.path")
          wait_said(killed, "building", 120)
          host.wait_until_succeeds("ps -eo user= | grep -q '^nixbld'", timeout=60)
          host.succeed("systemctl kill --signal=KILL valley-worker.service")
          host.wait_until_succeeds("test \"$(systemctl show -p ActiveState --value valley-worker)\" = failed", timeout=60)
          assert host.succeed(f"ls -A {wake}").split() == ["claimed", "lock"]
          host.wait_until_succeeds("! ps -eo user= | grep -q '^nixbld'", timeout=60)
          # Nothing watches claimed, so nothing starts a pass, until the
          # timer runs the recovery scan a minute after the kill.
          mark = started()
          host.sleep(5)
          assert started() == mark
          assert told(killed, "published") == 0
          host.succeed("systemctl start valley-worker.timer")
          wait_said(killed, "published", 300)
          idle()
          host.succeed(f"test ! -e {wake}/claimed")
          assert told(killed, "building") == 2

      with subtest("a pass that cannot reach the repositories leaves its claim, and the timer's next pass builds the change with no new mark"):
          host.succeed("systemctl stop valley-worker.path")
          ready("outage", "${spin}")
          outage = head("outage")
          host.succeed("systemctl stop sshd.service")
          host.succeed("systemctl start valley-worker.path")
          host.wait_until_succeeds("test \"$(systemctl show -p ActiveState --value valley-worker)\" = failed", timeout=120)
          assert host.succeed(f"ls -A {wake}").split() == ["claimed", "lock"]
          assert told(outage, "building") == 0
          triggered = unit_property("valley-worker.timer", "LastTriggerUSecMonotonic")
          host.succeed("systemctl start sshd.service")
          wait_said(outage, "published", 300)
          assert unit_property("valley-worker.timer", "LastTriggerUSecMonotonic") != triggered
          idle()
          host.succeed(f"test ! -e {wake}/claimed")

      with subtest("a failed batch is not built again before its wait, however many marks and timer passes come"):
          ready("broken", "${broken}")
          broken = head("broken")
          wait_said(broken, "the batch did not pass", 120)
          idle()
          ready("unrelated", "${spin}")
          wait_said(head("unrelated"), "published", 120)
          idle()
          host.succeed(
              f"{git} -C /srv/git/instance.git update-ref -d refs/the-valley/signers",
              "systemctl start valley-signers.service",
          )
          idle()
          host.succeed(f"cd /root/instance && echo again >> probe && {commit} -am again && GIT_SSH_COMMAND={ssh} git push --quiet origin floor-probe")
          idle()
          triggered = unit_property("valley-worker.timer", "LastTriggerUSecMonotonic")
          host.wait_until_succeeds(
              f"test \"$(systemctl show -p LastTriggerUSecMonotonic --value valley-worker.timer)\" != '{triggered}'", timeout=150
          )
          idle()
          assert told(broken, "building") == 1
          assert told(broken, "the batch did not pass") == 1
          assert told(broken, "its batch") >= 4, journal()
          # The scenarios below start each pass themselves.
          host.succeed("systemctl stop valley-worker.timer valley-worker.path")
          host.wait_until_succeeds("test \"$(systemctl show -p ActiveState --value valley-worker)\" != activating")

      with subtest("the slice holds its default caps"):
          cgroup = "/sys/fs/cgroup/valley.slice/valley-builds.slice"
          assert host.succeed(f"cat {cgroup}/cpu.max").strip() == "400000 100000"
          assert host.succeed(f"cat {cgroup}/memory.max").strip() == str(8 * 1024**3)
          assert host.succeed(f"cat {cgroup}/memory.swap.max").strip() == "0"

      with subtest("a build that wants six CPUs is held to the slice's quota"):
          # The machine running this test may cap the VM's own CPU, and no
          # quota above that can throttle. One CPU is always below it.
          host.succeed("systemctl set-property --runtime valley-builds.slice CPUQuota=100%")
          unrelated("cpu")
          host.wait_until_succeeds("test \"$(ps -eo user= | grep -c '^nixbld')\" -ge 7", timeout=60)
          # The quota is enforced every 100 ms, so five seconds holds fifty
          # of its periods.
          host.sleep(1)
          stat = lambda: dict(line.split() for line in host.succeed(f"cat {cgroup}/cpu.stat").splitlines())
          before = stat()
          host.sleep(5)
          after = stat()
          cpus = (int(after["usage_usec"]) - int(before["usage_usec"])) / 5e6
          assert 0.8 < cpus < 1.5, cpus
          assert int(after["nr_throttled"]) > int(before["nr_throttled"]), (before, after)
          host.succeed("systemctl stop unrelated-cpu")
          host.succeed("systemctl set-property --runtime valley-builds.slice CPUQuota=400%")
          assert host.succeed(f"cat {cgroup}/cpu.max").strip() == "400000 100000"
          host.wait_until_succeeds("! ps -eo user= | grep -q '^nixbld'", timeout=60)

      with subtest("a runaway build is stopped at the memory cap, a bigger unrelated build is stopped with it, and the rest of the host stays healthy"):
          evidence = len(attestations())
          ready("runaway", "${runaway}")
          failed_before = set(host.succeed("systemctl list-units --state=failed --no-legend --plain | cut -d' ' -f1").split())
          # Two builds that are not the worker's, with room to make their
          # strings. The shell takes about twice a string's size while it
          # makes it, so what big holds is read once it stops growing.
          host.succeed("systemctl set-property --runtime valley-builds.slice MemoryMax=1536M")
          unrelated("small")
          unrelated("big")
          host.wait_until_succeeds("ps -eo user=,rss= | awk '$1 ~ /^nixbld/ && $2 > 100000' | grep -q .", timeout=60)
          biggest = lambda: int(host.succeed("ps -eo user=,rss= | awk '$1 ~ /^nixbld/ {print $2}' | sort -n | tail -1").strip())
          held = 0
          for _ in range(30):
              host.sleep(1)
              now = biggest()
              if now == held:
                  break
              held = now
          assert held > 100000, held
          # The cap is what the slice cannot give back, and half of what big
          # holds. The runaway reaches the cap while it is still smaller than
          # big.
          memory = dict(line.split() for line in host.succeed(f"cat {cgroup}/memory.stat").splitlines())
          cap = int(memory["anon"]) + int(memory["shmem"]) + held * 1024 // 2
          host.succeed(f"systemctl set-property --runtime valley-builds.slice MemoryMax={cap}")
          host.fail("systemctl start valley-worker.service")
          said = journal()
          assert "the batch did not pass" in said, said
          kills = host.succeed(f"sed -n 's/^oom_kill //p' {cgroup}/memory.events").strip()
          assert int(kills) >= 2, kills
          # Nothing for the runaway.
          assert len(attestations()) == evidence
          # The kernel killed the biggest process in the slice: the unrelated
          # big build, before the runaway grew past it, and then the runaway.
          # The small build was never the biggest. It is still running, and
          # once it is told to finish, it does.
          small = host.succeed("ps -eo pid=,user=,args= | awk '$2 ~ /^nixbld/ && /USR1/ {print $1}'").split()
          assert len(small) == 1, small
          host.succeed(f"kill -USR1 {small[0]}")
          host.wait_until_succeeds("test \"$(systemctl show -p SubState --value unrelated-small)\" = exited", timeout=60)
          assert unit_property("unrelated-small", "Result") == "success"
          host.wait_until_succeeds("test \"$(systemctl show -p ActiveState --value unrelated-big)\" = failed", timeout=60)
          # The units the host runs are as they were, the daemon still
          # answers, and the git user still serves pushes and fetches.
          host.succeed(
              "systemctl is-active sshd.service nix-daemon.service nix-daemon.socket"
              " valley-integrator@instance.service valley-integrator@project.service"
          )
          failed_after = set(host.succeed("systemctl list-units --state=failed --no-legend --plain | cut -d' ' -f1").split())
          assert failed_after <= failed_before | {"valley-worker.service", "unrelated-big.service"}, failed_after
          host.succeed("nix-store --store daemon --query --hash /run/current-system")
          host.succeed(f"{contributor} git ls-remote origin refs/heads/runaway | grep -q runaway")
          # The failed batch waits before it is built again.
          host.succeed("systemctl start valley-worker.service")
          assert "failed 1 time in a row, so it is not built again before" in journal()

      with subtest("the key is the same after many starts and a reboot, and is not said again"):
          assert host.succeed(f"ssh-keygen -lf {state}/key").strip() == fingerprint
          host.shutdown()
          host.start()
          host.wait_for_unit("multi-user.target")
          host.succeed("systemctl stop valley-worker.timer valley-worker.path")
          host.succeed("systemctl start valley-worker.service")
          assert host.succeed(f"ssh-keygen -lf {state}/key").strip() == fingerprint
          assert host.succeed(f"cat {state}/verifier").strip() == verifier
          host.fail("journalctl -b -u valley-worker -o cat | grep -q 'verifier line is'")
    '';
  };
}
