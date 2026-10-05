# The push boundary over a real sshd: one NixOS machine running the module,
# pushed to over ssh by keys that each act as a principal, or as none. The
# other hook checks set VALLEY_PRINCIPAL in a push's environment directly,
# which is what sshd does from a key's authorized_keys entry; this check is
# where that substitution is itself checked, along with every way a client
# could try to supply the principal itself.
#
# It also walks the request grant's handoff, the one sequence a host goes
# through once: a grant held by hand, the same grant compiled from the
# registry beside it, and the hand grant removed only once the compiled one
# works.
{
  pkgs,
  lib,
  self,
  ...
}:
let
  # Throwaway keys, made once at build time. They hold no authority
  # anywhere: the machine that trusts them exists for one test run.
  keys = pkgs.runCommand "valley-ssh-e2e-keys" { nativeBuildInputs = [ pkgs.openssh ]; } ''
    mkdir $out
    for who in integrator contributor requester operator stranger; do
      ssh-keygen -q -t ed25519 -N "" -C "$who" -f "$out/$who"
    done
  '';
  pub = who: lib.fileContents "${keys}/${who}.pub";

  declaration = pkgs.writeText "valley-ssh-e2e.cue" ''
    package valley

    projects: {
      // The instance repository: the registry is read from its main, so
      // it is protected, and only the integrator principal pushes it.
      "instance": protection: writers: ["integrator"]
      "project": protection: {}
    }
  '';

  # An authorized_keys file planted in the git user's home, tagging the
  # untagged key as the integrator. sshd must not read it for that user.
  hostileKeys = pkgs.writeText "valley-ssh-e2e-hostile-keys" ''
    environment="VALLEY_PRINCIPAL=integrator" ${pub "stranger"}
  '';

  # The registry the operator lands: the operator governs, pushes, and
  # holds request. The requester holds request only by hand.
  registry = pkgs.writeText "registry.cue" ''
    package identity

    boundaries: {
      "push": kind:     "git-push"
      "request": kind:  "request"
      "registry": kind: "registry"
    }
    genesis: "operator"
    principals: "operator": {
      kind: "human"
      keys: [{
        class:  "ssh-ed25519"
        bound:  "hardware"
        public: "${lib.concatStringsSep " " (lib.take 2 (lib.splitString " " (pub "operator")))}"
      }]
      grants: {
        push: boundary:    "push"
        request: boundary: "request"
        govern: boundary:  "registry"
      }
    }
  '';
in
{
  ssh-e2e = pkgs.testers.runNixOSTest {
    name = "valley-ssh-e2e";
    nodes.host =
      { lib, ... }:
      {
        imports = [ self.nixosModules.default ];
        environment.systemPackages = [ pkgs.git ];
        services.valley = {
          enable = true;
          config = declaration;
          authorizedKeys = [
            {
              principal = "integrator";
              key = pub "integrator";
            }
            {
              principal = "contributor";
              key = pub "contributor";
            }
            {
              principal = "requester";
              key = pub "requester";
            }
            # Declared under the tag the registry gives it, so the operator
            # can push before the first compilation.
            {
              principal = "operator";
              key = pub "operator";
            }
            # A key that names no principal.
            (pub "stranger")
          ];
          grants.request = [
            "operator"
            "requester"
          ];
          identity.enable = true;
          integrator.instanceProject = "instance";
        };
        # The same host with the hand grant removed, as the end of the
        # handoff leaves it.
        specialisation.handed-off.configuration.services.valley.grants.request = lib.mkForce [ ];
        # The same host with sshd misconfigured to accept the variables the
        # push boundary reads, which the module's assertions refuse. They
        # are switched off here so the git user's shell can be shown to
        # neutralise what sshd lets through anyway.
        specialisation.misconfigured.configuration = {
          services.openssh.settings.AcceptEnv = "VALLEY_PRINCIPAL GIT_*";
          assertions = lib.mkForce [ ];
        };
      };

    testScript = ''
      import shlex

      host.wait_for_unit("multi-user.target")
      host.wait_for_unit("sshd.service")
      host.wait_until_succeeds("test -L /srv/git/project.git/hooks/pre-receive")

      host.succeed(
          "mkdir -p /root/keys",
          "cp ${keys}/* /root/keys/",
          "chmod 600 /root/keys/*",
          "git config --global user.name valley-check",
          "git config --global user.email valley-check@localhost",
          "git init --quiet --initial-branch=main /root/work",
          "git -C /root/work commit --quiet --allow-empty -m one",
      )

      # Every push carries a commit the server has not seen, so that it is
      # a write the hook has to decide, and never an up-to-date no-op.
      def push(who, refspec, repo="project", env="", ssh=""):
          command = (
              f"ssh -i /root/keys/{who} -o IdentitiesOnly=yes -o BatchMode=yes"
              " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null " + ssh
          )
          return (
              "cd /root/work && git commit --quiet --allow-empty -m push && "
              f"{env} GIT_SSH_COMMAND={shlex.quote(command)}"
              f" git push --quiet git@localhost:{repo}.git {refspec} 2>&1"
          )

      def refused(command, says):
          out = host.fail(command)
          assert says in out, f"expected {says!r} in:\n{out}"

      request = "refs/the-valley/integration-requests/main/one"

      with subtest("the principal is the one the key's entry names"):
          host.succeed(push("contributor", "+HEAD:refs/heads/topic"))
          refused(push("contributor", f"HEAD:{request}"), "contributor may not write")
          refused(push("contributor", "HEAD:refs/heads/main"), "a protected ref of project")
          refused(push("stranger", "HEAD:refs/heads/main", repo="instance"), "<untagged key> may not write")
          host.succeed(push("integrator", "HEAD:refs/heads/main", repo="instance"))

      with subtest("a client cannot supply the principal itself"):
          spoof = "VALLEY_PRINCIPAL=integrator"
          send = "-o SendEnv=VALLEY_PRINCIPAL -o SetEnv=VALLEY_PRINCIPAL=integrator"
          refused(
              push("stranger", "HEAD:refs/heads/main", repo="instance", env=spoof, ssh=send),
              "<untagged key> may not write",
          )
          refused(push("contributor", f"HEAD:{request}", env=spoof, ssh=send), "contributor may not write")
          # A key file in the git user's home is not one sshd reads for it.
          host.succeed(
              "mkdir -p /srv/git/.ssh",
              "cp ${hostileKeys} /srv/git/.ssh/authorized_keys",
              "chown -R git:git /srv/git/.ssh",
          )
          refused(push("stranger", "HEAD:refs/heads/main", repo="instance"), "<untagged key> may not write")
          host.succeed("rm -r /srv/git/.ssh")
          # And no key reaches a shell to set anything with.
          host.fail(
              "ssh -i /root/keys/stranger -o IdentitiesOnly=yes -o BatchMode=yes"
              " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null git@localhost env"
          )

      with subtest("a host that has not converged takes no push, and still serves fetches"):
          host.succeed(
              "printf '#!/bin/sh\\nexit 0\\n' > /srv/git/project.git/hooks/pre-receive.hand",
              "mv -f /srv/git/project.git/hooks/pre-receive /srv/git/project.git/hooks/pre-receive.managed",
              "mv /srv/git/project.git/hooks/pre-receive.hand /srv/git/project.git/hooks/pre-receive",
              "chmod +x /srv/git/project.git/hooks/pre-receive",
              "chown -h git:git /srv/git/project.git/hooks/pre-receive",
          )
          host.fail("systemctl restart valley-init.service")
          refused(push("integrator", "HEAD:refs/heads/main", repo="instance"), "pushes to this host are paused")
          refused(push("contributor", "+HEAD:refs/heads/topic"), "pushes to this host are paused")
          host.succeed(
              "GIT_SSH_COMMAND='ssh -i /root/keys/stranger -o IdentitiesOnly=yes -o BatchMode=yes"
              " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null'"
              " git ls-remote git@localhost:project.git refs/heads/topic | grep -q topic"
          )
          host.succeed("rm /srv/git/project.git/hooks/pre-receive", "systemctl restart valley-init.service")
          host.succeed(push("contributor", "+HEAD:refs/heads/topic"))
          # The operator's hold outlasts a convergence, and only its
          # removal lifts it.
          host.succeed("sudo -u git touch /srv/git/.valley-hold", "systemctl restart valley-init.service")
          refused(push("contributor", "+HEAD:refs/heads/topic"), "pushes to this host are held by its operator")
          host.succeed("rm /srv/git/.valley-hold")
          host.succeed(push("contributor", "+HEAD:refs/heads/topic"))

      with subtest("before the registry compiles, the hand grant is the only one"):
          host.succeed(push("operator", f"HEAD:{request}"))
          host.succeed(push("requester", f"+HEAD:{request}"))
          host.fail("grep -q '^request ' /var/lib/valley-identity/grants")

      with subtest("the compiled grant is added to the hand grant"):
          host.succeed(
              "rm -rf /root/instance && git init --quiet --initial-branch=main /root/instance",
              "mkdir -p /root/instance/identity && cp ${registry} /root/instance/identity/registry.cue",
              "git -C /root/instance add -A && git -C /root/instance commit --quiet -m registry",
          )
          command = (
              "ssh -i /root/keys/integrator -o IdentitiesOnly=yes -o BatchMode=yes"
              " -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
          )
          host.succeed(
              f"cd /root/instance && GIT_SSH_COMMAND={shlex.quote(command)}"
              " git push --quiet --force git@localhost:instance.git HEAD:refs/heads/main"
          )
          host.succeed("systemctl start valley-identity.service")
          host.succeed("grep -qx 'request operator' /var/lib/valley-identity/grants")
          host.succeed(push("operator", f"+HEAD:{request}"))
          host.succeed(push("requester", f"+HEAD:{request}"))

      with subtest("what sshd lets through, the git user's shell does not"):
          host.succeed(
              "/run/booted-system/specialisation/misconfigured/bin/switch-to-configuration test",
              "systemctl restart valley-init.service",
          )
          spoof = "VALLEY_PRINCIPAL=integrator"
          refused(
              push("stranger", "+HEAD:refs/heads/main", repo="instance", env=spoof, ssh="-o SendEnv=VALLEY_PRINCIPAL"),
              "<untagged key> may not write",
          )
          # Sent through, these would point git's hooks at nothing, and the
          # protected main would take the push.
          hooks_off = "GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.hooksPath GIT_CONFIG_VALUE_0=/dev/null"
          refused(
              push("contributor", "+HEAD:refs/heads/main", env=hooks_off, ssh="-o SendEnv=GIT_CONFIG_*"),
              "a protected ref of project",
          )
          host.succeed(
              "/run/booted-system/bin/switch-to-configuration test",
              "systemctl restart valley-init.service",
          )

      with subtest("removing the hand grant leaves the compiled one standing"):
          host.succeed(
              "/run/booted-system/specialisation/handed-off/bin/switch-to-configuration test",
              "systemctl restart valley-init.service",
          )
          host.succeed("grep -qx 'request operator' /var/lib/valley-identity/grants")
          host.succeed(push("operator", f"+HEAD:{request}"))
          refused(push("requester", f"--delete {request}"), "requester holds none")
    '';
  };
}
