# The web publisher's sandbox, in a NixOS machine running the host module
# and the publisher's module beside it. The unit is the one the module
# renders, with a probe in place of the publisher, so the probe runs as the
# unit's dynamic user under the unit's own confinement. It must read the
# repositories the host serves and nothing more: it writes no repository,
# reads no key outside its state, and reads no repository the host does not
# declare, though the system's git configuration trusts every directory. The
# unit receives the bearer token as a systemd credential while the token
# file on disk stays readable by root alone. The unit can connect to its
# allowed IP address while connections to disallowed IPv4 and IPv6 listeners
# fail, even though root can reach all three listeners from outside the
# unit. The unit's ExecStartPre egress check passes inside the unit
# (allowing the probe to start) and fails when run by root outside the unit
# (where root's UDP datagram reaches the canary listener). Inside the unit,
# the C library resolves a host name through nscd from a DNS server on a
# disallowed address, while a UDP datagram sent directly from the unit to
# that DNS server fails with EPERM.
#
# The read links' wrapper, valley-web-link, runs as root against
# repositories the git user owns, though root's git trusts none of them
# without the wrapper's entries. tmpfiles makes the links lock beside the
# credential file, root's, and root refuses one it does not own. The
# wrapper issues a link for a keyless principal into a credential file only
# root writes and the publisher's group reads, and, with no receiver
# listening, says the link is pending and exits 1. A
# revoke of every link fails its delivery the same way; once a receiver
# listens, the same revoke again delivers it, and a link issued then
# reaches the receiver, read by the policy unit through its group.
#
# The two publisher units run as one dynamic user. A policy pass started
# while a content pass holds the state and cache directories they share
# succeeds, with the content pass's UID, and the content pass still owns
# and writes them after it.
{
  pkgs,
  lib,
  self,
  packages,
  ...
}:
let
  declaration = pkgs.writeText "valley-web-publisher-e2e.cue" ''
    package valley

    groups: core: name: "tools"
    projects: {
      // The instance repository, whose main the host module requires
      // protected.
      "valley": protection: refs: ["refs/heads/main"]
      "app": group: "core"
    }
  '';

  # The instance's main: an identity registry granting alice, who holds
  # no key, an edge read of every project at example.org, and app's
  # policy/read, which lets alice read it.
  registry = pkgs.writeText "registry.cue" ''
    package identity

    boundaries: {
    	registry: kind: "registry"
    	web: {kind: "edge-read", destination: "example.org"}
    }
    genesis: "operator"
    principals: operator: {
    	kind: "human"
    	keys: [{class: "ssh-ed25519", bound: "hardware", public: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIEDVqaZm+1Xx4noNlTYckB32kUMMUnS8eN74T/Luxo+L"}]
    	grants: govern: boundary: "registry"
    }
    principals: alice: {kind: "human", keys: [], grants: read: {boundary: "web", projects: "all"}}
    anyone: grants: read: {boundary: "web", projects: {app: "9da6809f038df92138c131cd16bed76d"}}
  '';
  appRead = pkgs.writeText "read.cue" ''
    package read

    project: "app"
    identity: "9da6809f038df92138c131cd16bed76d"
    readers: {anyone: true, alice: true}
  '';
  # Commits the files to a repository's main as the git user, by a fetch
  # into the bare repository, which runs no hook.
  seed = pkgs.writeShellApplication {
    name = "seed";
    runtimeInputs = [
      pkgs.git
      pkgs.coreutils
    ];
    text = ''
      work=$(mktemp -d)
      git -C "$work" init --quiet
      git -C "$work" config user.name operator
      git -C "$work" config user.email operator@example.org
      mkdir -p "$work/policy/instance" "$work/policy/read" "$work/identity"
      echo 'package verification' > "$work/policy/instance/floor.cue"
      cp ${registry} "$work/identity/registry.cue"
      git -C "$work" add --all
      git -C "$work" commit --quiet -m "the instance"
      git -C /srv/git/valley.git fetch --quiet "$work" +HEAD:refs/heads/main
      list=$(printf 'app\nvalley\n' | git -C /srv/git/valley.git hash-object -w --stdin)
      git -C /srv/git/valley.git update-ref refs/the-valley/projects "$list"
      rm -rf "$work"
      work=$(mktemp -d)
      git -C "$work" init --quiet
      git -C "$work" config user.name operator
      git -C "$work" config user.email operator@example.org
      mkdir -p "$work/policy/read"
      cp ${appRead} "$work/policy/read/read.cue"
      echo /srv/git/valley.git > "$work/policy/valley"
      git -C "$work" add --all
      git -C "$work" commit --quiet -m "app"
      git -C /srv/git/app.git fetch --quiet "$work" +HEAD:refs/heads/main
      rm -rf "$work"
    '';
  };

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
      # With /run/probe-hold, the probe is a content pass that holds its
      # state and cache directories until the file is removed, then checks
      # it still owns them and writes them.
      if [ -e /run/probe-hold ]; then
        touch "$STATE_DIRECTORY/holding" "$CACHE_DIRECTORY/holding"
        for _ in $(seq 1200); do
          [ -e /run/probe-hold ] || break
          sleep 0.1
        done
        [ ! -e /run/probe-hold ] || fail "the probe was not let go"
        owners=$(stat -L -c %u "$STATE_DIRECTORY" "$CACHE_DIRECTORY" | sort -u)
        test "$owners" = "$(id -u)" || fail "the state or cache directory is owned by $owners, not by the probe's $(id -u)"
        rm "$STATE_DIRECTORY/holding" "$CACHE_DIRECTORY/holding" || fail "the state or cache directory could not be written after the policy pass"
        touch "$STATE_DIRECTORY/held" "$CACHE_DIRECTORY/held" || fail "the state or cache directory could not be written after the policy pass"
        echo "probe: held its state"
        exit 0
      fi
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
      # Egress: the unit can connect to the allowed receiver address, and
      # connections to disallowed IPv4 and IPv6 addresses fail within the
      # 5-second timeout (whether rejected with EPERM or dropped).
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
      # DNS: the C library resolves names through nscd outside the unit,
      # while the unit itself cannot send UDP packets to the DNS server.
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
      # Local DNS server on a disallowed IP address; answers queries for
      # receiver.example.org.
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

      # Start TCP listeners on the allowed receiver address and on
      # disallowed IPv4 and IPv6 addresses, and verify that root can
      # reach all three.
      host.succeed(
          "ip addr add 192.0.2.1/32 dev lo",
          "ip addr add 192.0.2.2/32 dev lo",
          "ip -6 addr add 2001:db8::2/128 dev lo nodad",
          "ip addr add 198.51.100.1/32 dev lo",
          "ip addr add 192.0.2.53/32 dev lo",
      )
      # Each listener replies with its short label because socat splits
      # option arguments on colons, which would break IPv6 literals.
      listeners = [
          ("allowed", "192.0.2.1", "TCP4-LISTEN:8080,bind=192.0.2.1"),
          ("ipv4", "192.0.2.2", "TCP4-LISTEN:8080,bind=192.0.2.2"),
          ("ipv6", "2001:db8::2", "TCP6-LISTEN:8080,bind=[2001:db8::2]"),
      ]
      for label, addr, bind in listeners:
          host.succeed(f"systemd-run --unit=listener-{label} socat {bind},reuseaddr,fork SYSTEM:'echo listener {label}'")
      for label, addr, bind in listeners:
          host.wait_until_succeeds(f"timeout 5 bash -c 'exec 3<> /dev/tcp/{addr}/8080 && head -n 1 <&3' | grep -qx 'listener {label}'", timeout=30)

      # UDP listener on the canary address (198.51.100.1:9); appends any
      # datagram it receives to /run/canary.
      host.succeed("systemd-run --unit=canary socat -u UDP4-RECV:9,bind=198.51.100.1 OPEN:/run/canary,creat,append")
      host.wait_until_succeeds("ss -Hulnp | grep -q '198.51.100.1:9 '", timeout=30)

      # Start the DNS server on 192.0.2.53 and verify that root can query it.
      host.succeed("systemd-run --unit=dns dnsmasq --keep-in-foreground --no-resolv --no-hosts --bind-interfaces --listen-address=192.0.2.53 --address=/receiver.example.org/192.0.2.1")
      host.wait_until_succeeds("dig +short +time=2 +tries=1 @192.0.2.53 receiver.example.org | grep -qx 192.0.2.1", timeout=30)

      # Run the unit's egress check as root outside the unit's cgroup:
      # the datagram reaches the canary listener, so the check fails.
      status, refused = host.execute("${check} 2>&1")
      print(refused)
      assert status != 0, "the egress check passed outside the unit"
      assert "a datagram to 198.51.100.1 port 9 was sent" in refused, "the egress check refused for another reason"
      host.wait_until_succeeds("grep -qx canary /run/canary", timeout=30)

      # Verify the systemd unit properties for the IP address filter.
      shown = host.succeed("systemctl show -p IPAddressDeny -p IPAddressAllow valley-web-publish.service")
      print(shown)
      assert "IPAddressAllow=192.0.2.1/32" in shown, "the unit does not allow the receiver's address"
      deny = [line for line in shown.splitlines() if line.startswith("IPAddressDeny=")]
      assert deny and sorted(deny[0].removeprefix("IPAddressDeny=").split()) == ["0.0.0.0/0", "::/0"], "the unit does not deny every other address"

      host.succeed("runuser -u git -- ${lib.getExe seed}")

      host.succeed("systemctl start valley-web-publish.service")
      journal = host.succeed("journalctl -u valley-web-publish.service --no-pager")
      print(journal)
      assert "refused a datagram to 198.51.100.1 port 9 (EPERM)" in journal, "the egress check did not pass inside the unit"
      assert host.succeed("cat /run/canary") == "canary", "a datagram from the unit's check reached the canary"
      assert "probe: confined" in journal, "the probe did not finish"
      host.succeed("test -e /var/lib/valley-web-publish/written")
      host.fail("test -e /srv/git/app.git/probe")

      # Read links. Root's git refuses the git user's repositories once
      # the system's trust is reset, as the wrapper's entries reset it.
      refused = host.fail("GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=safe.directory GIT_CONFIG_VALUE_0= git -C /srv/git/valley.git rev-parse HEAD 2>&1")
      assert "dubious ownership" in refused, f"root's git refused the instance for another reason: {refused}"
      assert host.succeed("stat -c '%U:%G %a %F' /var/lib/valley-web-links /var/lib/valley-web-links/links /var/lib/valley-web-links/lock") == "root:valley-web-links 750 directory\nroot:valley-web-links 640 regular empty file\nroot:valley-web-links 640 regular empty file\n", "the credential file's or the links lock's owners, modes or types are wrong"

      # Root refuses a links lock it does not own, and changes nothing.
      host.succeed("chown nobody /var/lib/valley-web-links/lock")
      status, refused = host.execute("valley-web-link revoke --all 2>&1")
      print(refused)
      assert status == 1 and "/var/lib/valley-web-links/lock is owned by UID" in refused, "root took a links lock it does not own"
      host.succeed("test ! -s /var/lib/valley-web-links/links")
      host.succeed("chown root /var/lib/valley-web-links/lock")

      # With no receiver listening, an issue as root writes the link and
      # prints it once, and the delivery fails: the link is pending. The
      # test never prints the link.
      status, _ = host.execute("valley-web-link issue --principal alice --note vm > /root/link 2> /root/issue.err")
      issued = host.succeed("cat /root/issue.err")
      print(issued)
      assert status == 1, f"an undelivered issue exited {status}"
      assert "dubious ownership" not in issued, "root's git refused a repository through the wrapper"
      host.succeed("grep -Eqx 'https://example\\.org/-/redeem#lk-[0-9a-f]{16}\\.[A-Za-z0-9_-]{43}' /root/link")
      assert ": issued here; the edge does not accept it until a pass delivers it" in issued, "the issue did not report the link pending"
      assert host.succeed("stat -c '%U:%G %a' /var/lib/valley-web-links/links") == "root:valley-web-links 640\n", "the link command wrote the credential file with other owners or another mode"
      host.fail("valley-web-link list --check")

      # A revoke of every link whose delivery fails exits 1; once a
      # receiver listens, the same revoke again delivers it.
      status, revoked = host.execute("valley-web-link revoke --all 2>&1")
      print(revoked)
      assert status == 1 and "revoked here; the edge may accept it" in revoked, "an undelivered revoke --all did not report the revocation pending"
      host.succeed(
          "install -d -m 0700 /var/lib/receiver",
          "systemd-run --unit=receiver ${lib.getExe' packages.valley-web-receive "valley-web-receive"} --state /var/lib/receiver/store --listen 192.0.2.1:80 --instance host --destination example.org --verifiers /var/lib/valley-web-publish/verifier --token-file /run/keys/web-receiver-token",
      )
      host.wait_until_succeeds("ss -Htln | grep -q '192.0.2.1:80 '", timeout=30)
      retried = host.succeed("valley-web-link revoke --all 2>&1")
      print(retried)
      assert "the receiver holds every link's current state" in retried, "a repeated revoke --all did not deliver"
      host.fail("grep -q '^binding ' /var/lib/receiver/v2/authorization")

      # A link issued now reaches the receiver: the policy unit reads the
      # credential file through its group as it signs.
      host.succeed("valley-web-link issue --principal alice > /root/link")
      lk = host.succeed("sed 's/.*#\\(lk-[0-9a-f]*\\)\\..*/\\1/' /root/link").strip()
      host.succeed(f"grep -Eq '^binding {lk} alice [0-9a-f]{{64}}$' /var/lib/receiver/v2/authorization")

      # Both units run as one dynamic user. A policy pass while a content
      # pass holds the state and cache directories succeeds, with the
      # content pass's UID, and the content pass then finds them its own,
      # as the unit sees them, writes them, and ends.
      users = host.succeed("systemctl show -p User --value valley-web-publish.service valley-web-publish-policy.service")
      assert users.split() == ["valley-web-publish", "valley-web-publish"], f"the units run as {users.split()}"
      host.succeed("touch /run/probe-hold")
      host.succeed("systemctl start --no-block valley-web-publish.service")
      host.wait_until_succeeds("test -e /var/lib/private/valley-web-publish/holding", timeout=60)
      probe_uid = host.succeed("stat -c %u /proc/$(systemctl show -p MainPID --value valley-web-publish.service)").strip()
      since = host.succeed("date +%s").strip()
      host.succeed("valley-web-link revoke --all")
      host.fail(f"grep -q '^binding {lk} ' /var/lib/receiver/v2/authorization")
      policy_uids = host.succeed(f"journalctl _SYSTEMD_UNIT=valley-web-publish-policy.service --since=@{since} -o export | sed -n 's/^_UID=//p' | sort -u").split()
      assert policy_uids == [probe_uid], f"the policy pass ran as UID {policy_uids}, and the content pass as {probe_uid}"
      host.succeed("systemctl show -p ActiveState --value valley-web-publish.service | grep -qx activating")
      host.succeed("rm /run/probe-hold")
      host.wait_until_succeeds("systemctl show -p ActiveState --value valley-web-publish.service | grep -Eqx 'inactive|failed'", timeout=60)
      journal = host.succeed("journalctl -u valley-web-publish.service --no-pager")
      print(journal)
      result = host.succeed("systemctl show -p Result --value valley-web-publish.service").strip()
      assert result == "success" and "probe: held its state" in journal, f"the content pass that held its state ended with {result}"
      host.succeed("test -e /var/lib/private/valley-web-publish/held -a -e /var/cache/private/valley-web-publish/held")
    '';
  };
}
