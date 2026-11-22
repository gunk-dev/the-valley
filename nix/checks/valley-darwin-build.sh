# shellcheck shell=bash
# One client program's build phase for darwin/arm64 (darwin.nix). GOOS,
# GOARCH and CGO_ENABLED come from the derivation, and so do targets, the
# package's build targets. They are linked, so a symbol Darwin lacks fails
# here as it would on a Mac. go vet runs over every package of this
# repository they are built from: those of the main module and of the
# modules it replaces with their directories beside it. The vetted
# packages are kept for the output.
# shellcheck disable=SC2154,SC2086 # targets and vetted are lists of packages
mkdir -p "$TMPDIR/bin"
go build -o "$TMPDIR/bin/" $targets
vetted="$(go list -deps -f '{{with .Module}}{{if or .Main .Replace}}{{$.ImportPath}}{{end}}{{end}}' $targets)"
go vet $vetted
echo "$vetted" > "$TMPDIR/vetted"
