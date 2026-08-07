# bin/valley: that it stays a lint-clean script, what its review verb
# anchors a note to, and what its deriver makes of a policy and a diff.
{ pkgs, packages, ... }:
{
  # The CLI must stay a lint-clean script whose help verb answers
  # without a repo or a remote — the cheap end of its eviction clause
  # (dcr-74c3158); anything needing more graduates instead. The
  # completions are held to the same bar, and the installed package
  # must carry them at the auto-linked paths.
  valley-cli = pkgs.runCommand "valley-cli" {
    nativeBuildInputs = [
      pkgs.shellcheck
      pkgs.zsh
      packages.valley
    ];
    cliScript = ../../bin/valley;
    bashCompletion = ../../completions/valley.bash;
    zshCompletion = ../../completions/_valley;
    valley = packages.valley;
  } (builtins.readFile ./valley-cli.sh);

  # review's notes: the anchors are the whole claim, so they are
  # pinned here rather than left to the reader of bin/valley. The
  # session is real — a pty, real less, a pager filter that
  # restructures the diff, and an $EDITOR that inserts lines while
  # the pager is open — because every property under test is a
  # property of that arrangement: that v is handed the raw buffer
  # and not the rendering, that an inserted line becomes a note,
  # and that the file and line it names are the ones it sat on.
  review-notes = pkgs.runCommand "valley-review-notes" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.less
      pkgs.util-linux # script(1), for the pty less insists on
      packages.valley-script
    ];
    # The reviewer, as a script: four insertions and one edit of a
    # line that was already there. The edit is deliberately far
    # from every insertion, since -U0 would otherwise merge them
    # into one hunk and the note would be dropped with it.
    reviewer = pkgs.writeShellScript "valley-review-notes-editor" (
      builtins.readFile ./review-notes-editor.sh
    );
  } (builtins.readFile ./review-notes.sh);

  # The verification-policy deriver (dcr-f41f718). Composing the two
  # layers is CUE's job and is pinned by cue-vet; matching paths is
  # the script's own, and a path matcher is wrong exactly at the glob
  # boundaries — `*` inside one segment against `**` across segments,
  # `**` matching no segment at all — and at the sides of a diff a
  # naive reader drops: a rename's old path and a deletion. Each is
  # pinned by driving the real script over a scratch repository and
  # the scratch policy in examples/policy/deriver/, whose floor is
  # one glob boundary per class.
  policy-deriver = pkgs.runCommand "valley-policy-deriver" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.cue
      packages.valley-script
    ];
    fixtures = ../../examples/policy/deriver;
    schema = ../../schema/verification.cue;
  } (builtins.readFile ./policy-deriver.sh);
}
