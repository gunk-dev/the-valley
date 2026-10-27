# The web publisher's sandbox, in a NixOS machine running the host module
# and the publisher's module beside it. The unit is the one the module
# renders, with a probe in place of the publisher, so the probe runs as the
# unit's dynamic user under the unit's own confinement. It must read the
# repositories the host serves and nothing more: it writes no repository,
# reads no key outside its state, and reads no repository the host does not
# declare, though the system's git configuration trusts every directory. The
# unit receives the bearer token as a systemd credential while the token
# file on disk stays readable by root alone. The unit reaches the one
# address it is allowed, and no listener at an unrelated IPv4 or IPv6
# address, though root reaches every one of them, so the filter is attached
# on this kernel and not just configured. The unit's check before each pass
# proves the same: it passes inside the unit, so the probe starts, and the
# same check run by root outside the unit refuses, for root's datagram to
# the canary is sent and arrives. A name that only a DNS server the unit may
# not reach can answer resolves in the unit through the C library, while a
# datagram from the unit to that server is denied.
{
  pkgs,
  lib,
  self,
  ...
}:
let
  declaration = pkgs.writeText "valley-web-publisher-e2e.cue" ''
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
      pkgs.bash
      pkgs.git
      pkgs.coreutils
      pkgs.findutils
      pkgs.getent
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
      grep -qx "edge-token-0123456789abcdef0123456789" "$CREDENTIALS_DIRECTORY/receiver-token" ||
        fail "the receiver's token did not reach the unit as a credential"
      for key in /srv/git/.ssh/id_ed25519 /run/keys/backup-ssh-key /run/keys/web-receiver-token; do
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
      # Egress: the receiver's address, which the unit allows, and no
      # other. A refused destination may answer EPERM or drop the packet,
      # so both count, within the bound.
      reach() {
        # shellcheck disable=SC2016
        timeout 5 bash -c 'exec 3<> "/dev/tcp/$1/8080" && head -n 1 <&3' reach "$1"
      }
      test "$(reach 192.0.2.1)" = "listener allowed" ||
        fail "the allowed listener at 192.0.2.1 could not be reached"
      for addr in 192.0.2.2 2001:db8::2; do
        start=$SECONDS
        if reach "$addr" > /dev/null 2>&1; then
          fail "the unrelated listener at $addr was reached"
        fi
        test $((SECONDS - start)) -le 6 || fail "the connection to $addr took more than 6 seconds to fail"
      done
      # Names: the C library asks nscd, which resolves outside the unit,
      # and the unit itself may not reach the DNS server.
      test "''${GODEBUG-}" = netdns=cgo || fail "Go's lookups are not pinned to the C library"
      getent hosts receiver.example.org > "$STATE_DIRECTORY/getent" ||
        fail "receiver.example.org did not resolve through the C library"
      grep -q "^192\.0\.2\.1 " "$STATE_DIRECTORY/getent" ||
        fail "receiver.example.org resolved to $(cat "$STATE_DIRECTORY/getent")"
      # shellcheck disable=SC2016
      if LC_ALL=C timeout 5 bash -c 'printf query > /dev/udp/192.0.2.53/53' 2> "$STATE_DIRECTORY/dns"; then
        fail "a datagram reached the DNS server at 192.0.2.53"
      fi
      grep -q "write error: Operation not permitted" "$STATE_DIRECTORY/dns" ||
        fail "the datagram to 192.0.2.53 failed for another reason: $(cat "$STATE_DIRECTORY/dns")"
      touch "$STATE_DIRECTORY/written" || fail "the state directory could not be written"
      touch "$CACHE_DIRECTORY/written" || fail "the cache directory could not be written"
      echo "probe: confined"
    '';
  };
in
{
  web-publisher-e2e = pkgs.testers.runNixOSTest {
    name = "valley-web-publisher-e2e";
    nodes.host = {
      imports = [
        self.nixosModules.default
        self.nixosModules.web-publisher
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
          receiverTokenFile = "/run/keys/web-receiver-token";
          allowedAddresses = [ "192.0.2.1" ];
        };
      };
      systemd.services.valley-web-publish.serviceConfig.ExecStart = lib.mkForce (lib.getExe probe);
      networking.enableIPv6 = true;
      # The one DNS server, which answers for receiver.example.org at an
      # address the unit is denied.
      networking.nameservers = [ "192.0.2.53" ];
      environment.systemPackages = [
        pkgs.socat
        pkgs.dnsmasq
        pkgs.dig
      ];
    };

    testScript =
      { nodes, ... }:
      let
        check = nodes.host.systemd.services.valley-web-publish.serviceConfig.ExecStartPre;
      in
      ''
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
          "echo edge-token-0123456789abcdef0123456789 | install -m 0400 /dev/stdin /run/keys/web-receiver-token",
      )

      # A listener at the receiver's address, which the unit is allowed,
      # and at an IPv4 and an IPv6 address it is not. Each answers with a
      # line naming itself, and root reaches all three.
      host.succeed(
          "ip addr add 192.0.2.1/32 dev lo",
          "ip addr add 192.0.2.2/32 dev lo",
          "ip -6 addr add 2001:db8::2/128 dev lo nodad",
          "ip addr add 198.51.100.1/32 dev lo",
          "ip addr add 192.0.2.53/32 dev lo",
      )
      # socat splits its address at colons, so each listener answers with
      # a label rather than its address.
      listeners = [
          ("allowed", "192.0.2.1", "TCP4-LISTEN:8080,bind=192.0.2.1"),
          ("ipv4", "192.0.2.2", "TCP4-LISTEN:8080,bind=192.0.2.2"),
          ("ipv6", "2001:db8::2", "TCP6-LISTEN:8080,bind=[2001:db8::2]"),
      ]
      for label, addr, bind in listeners:
          host.succeed(f"systemd-run --unit=listener-{label} socat {bind},reuseaddr,fork SYSTEM:'echo listener {label}'")
      for label, addr, bind in listeners:
          host.wait_until_succeeds(f"timeout 5 bash -c 'exec 3<> /dev/tcp/{addr}/8080 && head -n 1 <&3' | grep -qx 'listener {label}'", timeout=30)

      # The canary, which the unit's check sends a datagram to. What
      # arrives is kept in /run/canary.
      host.succeed("systemd-run --unit=canary socat -u UDP4-RECV:9,bind=198.51.100.1 OPEN:/run/canary,creat,append")
      host.wait_until_succeeds("ss -Hulnp | grep -q '198.51.100.1:9 '", timeout=30)

      # The DNS server, which root asks directly.
      host.succeed("systemd-run --unit=dns dnsmasq --keep-in-foreground --no-resolv --no-hosts --bind-interfaces --listen-address=192.0.2.53 --address=/receiver.example.org/192.0.2.1")
      host.wait_until_succeeds("dig +short +time=2 +tries=1 @192.0.2.53 receiver.example.org | grep -qx 192.0.2.1", timeout=30)

      # The unit's check, run by root outside the unit, where no filter
      # denies the canary: its datagram is sent and arrives, and it refuses.
      status, refused = host.execute("${check} 2>&1")
      print(refused)
      assert status != 0, "the egress check passed outside the unit"
      assert "a datagram to 198.51.100.1 port 9 was sent" in refused, "the egress check refused for another reason"
      host.wait_until_succeeds("grep -qx canary /run/canary", timeout=30)

      # The filter as the module renders it: every address denied, and the
      # receiver's allowed.
      shown = host.succeed("systemctl show -p IPAddressDeny -p IPAddressAllow valley-web-publish.service")
      print(shown)
      assert "IPAddressAllow=192.0.2.1/32" in shown, "the unit does not allow the receiver's address"
      deny = [line for line in shown.splitlines() if line.startswith("IPAddressDeny=")]
      assert deny and sorted(deny[0].removeprefix("IPAddressDeny=").split()) == ["0.0.0.0/0", "::/0"], "the unit does not deny every other address"

      host.succeed("runuser -u git -- sh -c 'tree=$(git -C /srv/git/app.git mktree < /dev/null); tip=$(echo initial | git -C /srv/git/app.git -c user.name=operator -c user.email=operator@example.org commit-tree \"$tree\"); git -C /srv/git/app.git update-ref refs/heads/main \"$tip\"'")

      host.succeed("systemctl start valley-web-publish.service")
      journal = host.succeed("journalctl -u valley-web-publish.service --no-pager")
      print(journal)
      assert "refused a datagram to 198.51.100.1 port 9 (EPERM), so it is in force" in journal, "the egress check did not pass inside the unit"
      assert host.succeed("cat /run/canary") == "canary", "a datagram from the unit's check reached the canary"
      assert "probe: confined" in journal, "the probe did not finish"
      host.succeed("test -e /var/lib/valley-web-publish/written")
      host.fail("test -e /srv/git/app.git/probe")
    '';
  };
}
