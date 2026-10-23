# The check worker on a host: one NixOS machine running the module with the
# worker on, beside the controllers and the registry compiler, and a
# project with ready changes. The worker makes its key the first time it
# starts, readable by it alone, and says its public halves. Until the
# registry holds the key, the worker cannot reach the host's repositories.
# Once it does, a pass builds the nix checks a ready change requires, one
# build at a time, through the machine's nix daemon, and the evidence is on
# origin before anyone approves the change. The timer runs the next pass.
# Then a change whose check eats memory without end is built, and the
# kernel stops the build at the slice's memory cap while the rest of the
# host goes on as it was.
#
# The operator's caps are 4 CPUs and 8 GiB. The machine here has 2 GiB, so
# the memory cap is lowered at runtime, after the check reads the module's
# own values off the slice, to let a build run past it. module-eval holds
# the values the module renders.
{
  pkgs,
  lib,
  self,
  packages,
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
      "project": protection: {}
    }
  '';

  # A floor that requires two nix checks of every change.
  floor = pkgs.writeText "floor.cue" ''
    package verification

    floor: {
    	checks: {
    		first: {runner: "nix", attribute: "first"}
    		second: {runner: "nix", attribute: "second"}
    	}
    	classes: {}
    	unclassified: {first: true, second: true}
    }
  '';

  # The project's flake, with no inputs, so the machine builds it offline.
  # Each check runs a script from the project's tree in the sandbox's
  # shell, and the script is the derivation's one input. The project's own
  # scripts spin for a few seconds, so two builds at once would be seen at
  # once, and write the cores the daemon gave them. The runaway change's
  # first script instead doubles a string until something stops it.
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
    while [ $i -lt 1000000 ]; do i=$((i+1)); done
    echo $NIX_BUILD_CORES > $out
  '';
  runaway = pkgs.writeText "runaway.sh" ''
    x=a
    while :; do x=$x$x; done
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
      # Two CPUs, so attest could run two builds at once if the unit let it.
      virtualisation.cores = 2;
      virtualisation.memorySize = 2048;
      environment.systemPackages = [
        pkgs.git
        packages.valley
      ];
      services.valley = {
        enable = true;
        config = declaration;
        authorizedKeys = [
          {
            principal = "contributor";
            key = pub "contributor";
          }
        ];
        identity.enable = true;
        integrator = {
          enable = true;
          signingKeyFile = "${keys}/integrator";
          instanceProject = "instance";
          signerProjects."worker/attestations" = [ "project" ];
        };
        worker = {
          enable = true;
          projects = [ "project" ];
          signerName = "worker/attestations";
        };
      };
      # NixOS test machines panic on any OOM, a cgroup's included
      # (test-instrumentation.nix). A host does not, and the runaway build
      # below is stopped by the slice's OOM, so a host's default is restored.
      boot.kernel.sysctl."vm.panic_on_oom" = 0;
      # The test starts each pass itself until it checks the timer, so no
      # pass runs before the test is ready for it.
      systemd.timers.valley-worker.timerConfig.OnBootSec = lib.mkForce "1h";
    };

    testScript = ''
      import shlex

      host.wait_for_unit("multi-user.target")
      host.succeed("systemctl is-active valley-worker.timer")
      host.succeed("systemctl stop valley-worker.timer")

      git = "sudo -u git git"
      state = "/var/lib/valley-worker"
      ssh = shlex.quote(
          "ssh -i /root/keys/contributor -o IdentitiesOnly=yes -o BatchMode=yes"
          " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
      )
      contributor = f"cd /root/project && GIT_SSH_COMMAND={ssh}"
      commit = "git -c user.name=contributor -c user.email=contributor@localhost commit --quiet"

      def journal():
          return host.succeed("journalctl -u valley-worker -o cat")

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
          assert attestations() == []

      with subtest("enrolled in the registry, the worker builds the ready change's checks one at a time and publishes them"):
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
          # One build at a time, each in the daemon's cgroup, inside the
          # slice, each told it may use 4 cores.
          builders = [int(n) for n in host.succeed("cat /tmp/builders").split()]
          assert max(builders) == 1, builders
          cgroups = set(host.succeed("cat /tmp/build-cgroups").split())
          assert cgroups and all(c.startswith("0::/valley.slice/valley-builds.slice/nix-daemon.service") for c in cgroups), cgroups
          for check in ["first", "second"]:
              assert host.succeed(f"cat /nix/store/*-worker-{check}").strip() == "4", check
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

      with subtest("the slice holds the operator's caps"):
          cgroup = "/sys/fs/cgroup/valley.slice/valley-builds.slice"
          assert host.succeed(f"cat {cgroup}/cpu.max").strip() == "400000 100000"
          assert host.succeed(f"cat {cgroup}/memory.max").strip() == str(8 * 1024**3)
          assert host.succeed(f"cat {cgroup}/memory.swap.max").strip() == "0"

      with subtest("a runaway build is stopped at the memory cap, and the rest of the host stays healthy"):
          ready("runaway", "${runaway}")
          host.succeed("systemctl set-property --runtime valley-builds.slice MemoryMax=512M")
          assert host.succeed(f"cat {cgroup}/memory.max").strip() == str(512 * 1024**2)
          failed_before = set(host.succeed("systemctl list-units --state=failed --no-legend --plain | cut -d' ' -f1").split())
          host.fail("systemctl start valley-worker.service")
          said = journal()
          assert "the batch did not pass" in said, said
          kills = host.succeed(f"sed -n 's/^oom_kill //p' {cgroup}/memory.events").strip()
          assert int(kills) >= 1, kills
          assert len(attestations()) == 1
          # The units the host runs are as they were, the daemon still
          # answers, and the git user still serves pushes and fetches.
          host.succeed(
              "systemctl is-active sshd.service nix-daemon.service nix-daemon.socket"
              " valley-integrator@instance.service valley-integrator@project.service"
          )
          failed_after = set(host.succeed("systemctl list-units --state=failed --no-legend --plain | cut -d' ' -f1").split())
          assert failed_after <= failed_before | {"valley-worker.service"}, failed_after
          host.succeed("nix-store --store daemon --query --hash /run/current-system")
          host.succeed(f"{contributor} git ls-remote origin refs/heads/runaway | grep -q runaway")
          # The failed batch waits before it is built again.
          host.succeed("systemctl start valley-worker.service")
          assert "failed 1 time in a row, so it is not built again before" in journal()
    '';
  };
}
