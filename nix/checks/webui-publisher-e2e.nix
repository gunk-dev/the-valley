# The web publisher's sandbox, in a NixOS machine running the host module
# and the publisher's module beside it. The unit is the one the module
# renders, with a probe in place of the publisher, so the probe runs as the
# unit's dynamic user under the unit's own confinement. It must read the
# repositories the host serves and nothing more: it writes no repository,
# reads no key outside its state, and reads no repository the host does
# not declare, though the system's git configuration trusts every
# directory.
{
  pkgs,
  lib,
  self,
  ...
}:
let
  declaration = pkgs.writeText "valley-webui-publisher-e2e.cue" ''
    package valley

    projects: {
      // The instance repository, whose main the host module requires
      // protected.
      "valley": protection: refs: ["refs/heads/main"]
      "app": {}
    }
  '';

  # What the unit runs in place of the publisher. Each check is one claim
  # about the sandbox, and the probe says which one failed.
  probe = pkgs.writeShellApplication {
    name = "valley-web-publish-probe";
    runtimeInputs = [
      pkgs.git
      pkgs.coreutils
      pkgs.findutils
      pkgs.gnugrep
    ];
    text = ''
      fail() {
        echo "probe: $*" >&2
        exit 1
      }
      git -C /srv/git/app.git rev-parse --git-dir > /dev/null ||
        fail "a repository the host serves could not be read"
      if git -C /srv/git/stray.git rev-parse --git-dir 2> "$STATE_DIRECTORY/stray"; then
        fail "a repository the host does not declare was read"
      fi
      grep -q "dubious ownership" "$STATE_DIRECTORY/stray" ||
        fail "the undeclared repository was refused for another reason: $(cat "$STATE_DIRECTORY/stray")"
      if touch /srv/git/app.git/probe 2> /dev/null; then
        fail "a repository was written"
      fi
      if git -C /srv/git/app.git update-ref refs/heads/probe refs/heads/main 2> /dev/null; then
        fail "a repository's ref was written"
      fi
      for key in /srv/git/.ssh/id_ed25519 /run/keys/backup-ssh-key; do
        if cat "$key" > /dev/null 2>&1; then
          fail "$key was read"
        fi
      done
      # The same alternate layout the publisher writes must resolve host
      # objects with the exact safe.directory list and read-only mounts.
      shared="$CACHE_DIRECTORY/shared.git"
      git init --quiet --bare --template= "$shared"
      git -C /srv/git/app.git rev-parse --path-format=absolute --git-path objects > "$shared/objects/info/alternates"
      tip=$(git -C /srv/git/app.git rev-parse refs/heads/main)
      git -C "$shared" update-ref refs/heads/main "$tip"
      git -C "$shared" cat-file -e "$tip^{commit}" || fail "shared objects could not be read"
      test "$(find "$shared/objects" -type f | wc -l)" -eq 1 || fail "objects were copied"
      # The CLI uses a worktree-shaped cache with the same borrowed objects.
      repos="$CACHE_DIRECTORY/repos"
      mkdir "$repos"
      cli="$repos/app"
      git init --quiet --template= "$cli"
      cp "$shared/objects/info/alternates" "$cli/.git/objects/info/alternates"
      git -C "$cli" config remote.origin.url /srv/git/app.git
      git -C "$cli" config core.logallrefupdates false
      chmod 0500 "$repos"
      if mkdir "$repos/unlisted" 2> /dev/null; then
        fail "an unstaged CLI cache was created"
      fi
      git -C "$cli" update-ref refs/remotes/origin/main "$tip"
      git -C "$cli" cat-file -e "$tip^{commit}" || fail "CLI shared objects could not be read"
      test "$(find "$cli/.git/objects" -type f | wc -l)" -eq 1 || fail "CLI objects were copied"
      chmod 0700 "$repos"
      touch "$STATE_DIRECTORY/written" || fail "the state directory could not be written"
      touch "$CACHE_DIRECTORY/written" || fail "the cache directory could not be written"
      echo "probe: confined"
    '';
  };
in
{
  webui-publisher-e2e = pkgs.testers.runNixOSTest {
    name = "valley-webui-publisher-e2e";
    nodes.host = {
      imports = [
        self.nixosModules.default
        self.nixosModules.webui-publisher
      ];
      # The system configuration trusts every directory, so only the
      # entries in the unit's environment can keep a repository out.
      environment.etc.gitconfig.text = ''
        [safe]
        	directory = *
      '';
      services.valley = {
        enable = true;
        config = declaration;
        # The host module requires a key for the git user. Nobody holds
        # this one's private half, and nothing in the test pushes.
        authorizedKeys = [
          "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEDVqaZm+1Xx4noNlTYckB32kUMMUnS8eN74T/Luxo+L operator"
        ];
        integrator.instanceProject = "valley";
        backup.sshKeyFile = "/run/keys/backup-ssh-key";
        webPublisher = {
          enable = true;
          destination = "example.org";
          receiver = "http://192.0.2.1";
        };
      };
      systemd.services.valley-web-publish.serviceConfig.ExecStart = lib.mkForce (lib.getExe probe);
    };

    testScript = ''
      host.wait_for_unit("multi-user.target")
      host.wait_until_succeeds("test -d /srv/git/app.git && test -d /srv/git/valley.git")

      # A repository the host does not declare, as the git user would
      # leave one, and two keys the publisher must not read. Each is
      # readable by the git group or by anyone, so only the unit's
      # confinement keeps the probe from it.
      host.succeed(
          "runuser -u git -- git init --quiet --bare /srv/git/stray.git",
          "install -d -m 0750 -o git -g git /srv/git/.ssh",
          "echo secret | install -m 0640 -o git -g git /dev/stdin /srv/git/.ssh/id_ed25519",
          "install -d -m 0755 /run/keys",
          "echo secret | install -m 0644 /dev/stdin /run/keys/backup-ssh-key",
      )

      host.succeed("runuser -u git -- sh -c 'tree=$(git -C /srv/git/app.git mktree < /dev/null); tip=$(echo initial | git -C /srv/git/app.git -c user.name=operator -c user.email=operator@example.org commit-tree \"$tree\"); git -C /srv/git/app.git update-ref refs/heads/main \"$tip\"'")

      host.succeed("systemctl start valley-web-publish.service")
      journal = host.succeed("journalctl -u valley-web-publish.service --no-pager")
      print(journal)
      assert "probe: confined" in journal, "the probe did not finish"
      host.succeed("test -e /var/lib/valley-web-publish/written")
      host.fail("test -e /srv/git/app.git/probe")
    '';
  };
}
