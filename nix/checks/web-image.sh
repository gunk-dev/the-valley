# shellcheck shell=bash
# The image comes from the derivation environment.
# shellcheck disable=SC2154
fail() {
  echo "web-image: $*" >&2
  exit 1
}
mkdir image root
tar -xzf "$image" -C image
config=image/$(jq -r '.[0].Config' image/manifest.json)
[ "$(jq -r '.config.User' "$config")" = valley-web ] ||
  fail "the image does not run as valley-web unless it is told otherwise"
jq -e '.config.Volumes | keys == ["/var/lib/valley-web"]' "$config" > /dev/null ||
  fail "the image's one volume is not /var/lib/valley-web"
jq -e '.config.Entrypoint == null and .config.Cmd == null' "$config" > /dev/null ||
  fail "the image starts a program of its own"

# Unpack every layer in order and record each entry's owner and mode from
# the tar headers, since unprivileged extraction drops ownership.
for layer in $(jq -r '.[0].Layers[]' image/manifest.json); do
  tar --numeric-owner --absolute-names -tvf "image/$layer" >> listing
  tar -xf "image/$layer" -C root 2> /dev/null
  chmod -R u+w root
done
# Resolve a root-level symlink inside the unpacked image so its target
# stays under root/.
inside() {
  local link
  link=$(readlink "root$1") || link=$1
  echo "root$link"
}
grep -Eq '^drwxr-x--- 990/990 .* (\./)?var/lib/valley-web/?$' listing ||
  fail "the state directory is not the receiver's, mode 0750: $(grep 'var/lib/valley-web' listing)"
grep -qx 'valley-web-receive:x:990:990:valley-web-receive:/var/empty:/bin/false' "$(inside /etc/passwd)" ||
  fail "/etc/passwd does not name the receiver's user"
grep -qx 'valley-web:x:991:990:valley-web:/var/empty:/bin/false' "$(inside /etc/passwd)" ||
  fail "/etc/passwd does not name valley-web's user"
grep -qx 'valley-web:x:990:' "$(inside /etc/group)" || fail "/etc/group does not name the valley-web group"
# Each program runs git at the store path it was built with, not at
# /bin/git, so the image must hold that store path.
for program in valley-web valley-web-receive; do
  link=$(readlink "root/bin/$program") || fail "/bin/$program is missing"
  [ -x "root$link" ] || fail "/bin/$program points at $link, which the image does not hold"
  git=$(grep -ao '/nix/store/[a-z0-9]\{32\}-git-[^/]*/bin/git' "root$link" | head -n 1)
  [ -n "$git" ] || fail "$program names no git"
  [ -x "root$git" ] || fail "$program runs $git, which the image does not hold"
done
echo "web-image: the image holds both programs, the git they run, their users and the state directory"
touch "$out"
