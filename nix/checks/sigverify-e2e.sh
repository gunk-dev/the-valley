# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
#
# The shipped sigverify over a real repository, called the way cosmo calls
# it to verify a release tag. ssh-keygen signs through the sk-dummy
# authenticator the derivation names in SSH_SK_PROVIDER.
export HOME="$TMPDIR"
export GIT_CONFIG_NOSYSTEM=1
git config --global user.name valley-check
git config --global user.email valley-check@localhost
git config --global init.defaultBranch main
git config --global gpg.format ssh

# Two security keys that differ in one respect: the first asks the
# authenticator for a touch, and the second does not. Both are listed.
ssh-keygen -q -t ed25519-sk -N "" -C touched -f "$TMPDIR/touched"
ssh-keygen -q -t ed25519-sk -N "" -C untouched -O no-touch-required -f "$TMPDIR/untouched"
{
  echo "release@valley.invalid $(cat "$TMPDIR/touched.pub")"
  echo "release@valley.invalid $(cat "$TMPDIR/untouched.pub")"
} > "$TMPDIR/allowed_signers"
git config --global gpg.ssh.allowedSignersFile "$TMPDIR/allowed_signers"

git init --quiet "$TMPDIR/repo"
cd "$TMPDIR/repo" || exit 1
echo release > file
git add file
git commit --quiet -m release
git -c user.signingKey="$TMPDIR/touched" tag -s v1 -m 'release 1'
git -c user.signingKey="$TMPDIR/untouched" tag -s v2 -m 'release 2'

verify() {
  status=0
  sigverify git-tag --repo "$TMPDIR/repo" --tag "$1" \
    --allowed-signers "$TMPDIR/allowed_signers" > "$1.out" 2> "$1.err" || status=$?
}

# A touched tag is verified, and the output names the commit to apply.
verify v1
if [ "$status" -ne 0 ]; then
  echo "sigverify-e2e: the touched tag was not verified" >&2
  cat v1.out v1.err >&2
  exit 1
fi
grep -qx verified v1.out
grep -qx "object $(git rev-parse HEAD)" v1.out
grep -qx 'user-presence yes' v1.out

# git verify-tag accepts the untouched tag. sigverify refuses it, with
# exit status 1.
if ! git verify-tag v2 2> v2.git; then
  echo "sigverify-e2e: git verify-tag refused the untouched tag, so the gap is closed upstream" >&2
  cat v2.git >&2
  exit 1
fi
verify v2
if [ "$status" -ne 1 ]; then
  echo "sigverify-e2e: the untouched tag gave exit status $status, not 1" >&2
  cat v2.out v2.err >&2
  exit 1
fi
grep -qx 'refused no-user-presence' v2.out

# A tag that does not exist is an error, exit status 2: nothing was
# verified or refused.
verify v3
if [ "$status" -ne 2 ]; then
  echo "sigverify-e2e: a missing tag gave exit status $status, not 2" >&2
  exit 1
fi

echo "sigverify-e2e: a touched tag verified, an untouched one refused"
touch "$out"
