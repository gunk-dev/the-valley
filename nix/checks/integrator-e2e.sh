# shellcheck shell=bash
# Paths and fixtures come from the derivation environment.
# shellcheck disable=SC2154
cp -r "$e2eDir" e2e
chmod -R +w e2e
# The stand-ins go on PATH and are executed by name, so
# their /usr/bin/env shebangs have to resolve in a sandbox
# that has no /usr/bin.
patchShebangs e2e
bash e2e/run.sh 2>&1 | tee log
grep -q 'every scenario held' log
cp log "$out"
