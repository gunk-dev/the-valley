# bin/valley: that it stays a lint-clean script, what its review verb
# anchors a note to, what its deriver makes of a policy and a diff, that
# the-valley's own policy composes and names only checks that exist, what
# its review verb publishes when it files an integration request, what its
# Go verbs' unit tests hold, what its ready and withdraw verbs write to a
# change's record through the real push hook, and that its package
# evaluates for aarch64-darwin.
{
  pkgs,
  self,
  system,
  packages,
  hosts,
  ...
}:
let
  # A stand-in for git, sort or cue that fails one chosen call; see
  # fail-nth.sh.
  failNth = pkgs.writeShellScript "fail-nth" (builtins.readFile ./fail-nth.sh);
in
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
  # one glob boundary per class. A check's cost is pinned the same way:
  # marking a check slow moves it between the summary lines and the
  # --cost lists, and changes nothing owed.
  #
  # And the deriver fails closed: failNth stands in for git, sort and
  # cue, and fails each call the deriver makes in turn. Every failure
  # must end the derivation with nothing on stdout, because a failure
  # read as an empty list is a change that owes nothing. cliScript is
  # bin/valley itself, because the package's wrapper puts its own git
  # ahead of anything on PATH.
  policy-deriver = pkgs.runCommand "valley-policy-deriver" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.cue
      packages.valley-script
    ];
    fixtures = ../../examples/policy/deriver;
    schema = ../../schema/verification.cue;
    cliScript = ../../bin/valley;
    inherit failNth;
  } (builtins.readFile ./policy-deriver.sh);

  # the-valley's own project layer (policy/project), which is what gates
  # every change to this repository. A layer that stopped composing would
  # stop every derivation once it landed, the derivation of its own fix
  # included, so it is held here before it lands. The floor it is composed
  # with is the example one: the owning valley's is not in this tree. The
  # check also holds every check the layer names to being an attribute of
  # this flake's checks, and holds a docs-only change to owing
  # prose-format alone. And it holds a check's own files to owing that
  # check: every file in this directory named after a check, and every
  # other script here, read from the directory itself so a new one is
  # held as soon as it is added.
  own-policy = pkgs.runCommand "valley-own-policy" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.cue
      packages.valley-script
    ];
    schema = ../../schema/verification.cue;
    project = ../../policy/project;
    floor = ../../examples/policy/instance/floor-format.cue;
    template = ../../examples/policy/instance/template-docs.cue;
    checkNames = builtins.attrNames self.checks.${system};
    checkFiles = builtins.attrNames (builtins.readDir ./.);
  } (builtins.readFile ./own-policy.sh);

  # review's [a]pprove and the approve and rebase verbs, driven end to end
  # over scratch repositories: a branch reviewed, the request filed, and
  # what the receiving side then holds. The claim is about sequencing, so
  # it is only observable from there — the bare repo logs every push it
  # receives, and the check reads that log to see that ONE of them carried
  # the evidence and the request together. Every check the scratch policy
  # declares uses the command runner, for the reason attest-e2e gives: a
  # nix build has no daemon. The packaged CLI carries its own schema, so
  # the scratch projects that stand for projects other than the-valley
  # carry no schema/. cliScript is bin/valley itself, for its other
  # shipping mode: run straight from a the-valley checkout, it reads the
  # schema there.
  valley-request = pkgs.runCommand "valley-request" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.cue
      pkgs.openssh
      packages.attest
      packages.valley-script
    ];
    schemaFile = ../../schema/verification.cue;
    cliScript = ../../bin/valley;
    inherit failNth;
  } (builtins.readFile ./valley-request.sh);

  # The Go verbs' tests (status/), run in valley-go's checkPhase: the
  # outcome records status accepts, the names its JSON refuses, and its
  # shell quoting held to the bash it replaced. valley-status and
  # valley-ready drive the verbs through the CLI over real repositories.
  valley-go-unit = packages.valley-go;
  # The name main's policy still owes for status/**, so a change can be
  # approved while the rename lands; a later change drops it once the
  # renamed policy is on main.
  valley-status-unit = packages.valley-go;

  # Read origin's request/outcome refs and distinguish a pending request,
  # a recorded decision and a landing, without moving the user's checkout.
  # Each state is read again through --json, and jq holds the object to
  # the valley-status/v1 contract.
  valley-status = pkgs.runCommand "valley-status" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      packages.valley-script
    ];
  } (builtins.readFile ./valley-status.sh);

  # ready and withdraw, through real pushes to a bare repo wired with the
  # rendered pre-receive hook, as each principal: a change created at v1,
  # a second version, the same head refused, a withdrawal and the new
  # version that revives it, a principal that neither owns the change nor
  # holds the request grant refused by the hook, and the operator, who
  # holds it, let through. Then each source of a change's ID — the jj
  # header, the Change-Id trailer, a generated ID, --stamp — a header and a
  # trailer that disagree, and a change whose commits hold a merge. Last, a
  # change named by its handle is approved, reviewed and rebased at the head
  # its record names: a record pushed with its branch moved elsewhere in the
  # same push is refused, the branch at the recorded head is filed at that
  # head, and a later version whose branch is elsewhere is refused. The hook
  # is followed from the protected host's rendered init script, so the rules
  # the record meets are the ones a host applies.
  valley-ready = pkgs.runCommand "valley-ready" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      packages.valley-script
    ];
    initScript = hosts.protectedHost.config.systemd.services.valley-init.script;
    passAsFile = [ "initScript" ];
  } (builtins.readFile ./valley-ready.sh);

  # The CLI is offered for aarch64-darwin, which nothing here builds or
  # tests (flake.nix). This check holds that offer to evaluating: an input
  # with no Darwin build fails here rather than on a Mac. The package's
  # drvPath is forced while this check is evaluated, and its string context
  # is discarded, so the Darwin derivations are written to the store but
  # are not inputs of this one. Building this check builds and substitutes
  # nothing for Darwin.
  valley-darwin-eval = pkgs.runCommand "valley-darwin-eval" {
    darwinDrv = builtins.unsafeDiscardStringContext self.packages.aarch64-darwin.valley.drvPath;
    darwinSystem = self.packages.aarch64-darwin.valley.system;
  } (builtins.readFile ./valley-darwin-eval.sh);
}
