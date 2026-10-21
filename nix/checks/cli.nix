# bin/valley: that it stays a lint-clean script, what its review verb
# anchors a note to, what its deriver makes of a policy and a diff, that
# the-valley's own policy composes and names only checks that exist, what
# its review verb publishes when it files an integration request, what its
# Go verbs' unit tests and the rebase engine's hold, what its ready and withdraw verbs write to a
# change's record through the real push hook, what its knot verbs write to
# the plan graph, what its plan verb shows of that graph, what its
# self-update verb installs and what the home-manager module runs it with,
# and that its package evaluates for aarch64-darwin.
{
  pkgs,
  lib,
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

  # The user units the home-manager module renders for a home with
  # self-update on. The module is evaluated over stand-ins for the
  # home-manager options it sets, because this flake has no home-manager.
  selfUpdateUnits =
    (lib.evalModules {
      modules = [
        self.homeManagerModules.valley
        {
          options = {
            systemd.user.services = lib.mkOption { type = lib.types.attrsOf lib.types.anything; };
            systemd.user.timers = lib.mkOption { type = lib.types.attrsOf lib.types.anything; };
            home.homeDirectory = lib.mkOption { type = lib.types.str; };
            home.username = lib.mkOption { type = lib.types.str; };
          };
          config = {
            _module.args.pkgs = pkgs;
            home.homeDirectory = "/home/someone";
            home.username = "someone";
            programs.valley.selfUpdate = {
              enable = true;
              repo = "git@classic-laddie:the-valley";
            };
          };
        }
      ];
    }).config.systemd.user;
in
{
  # The CLI must stay a lint-clean script whose help verb answers
  # without a repo or a remote — the cheap end of its eviction clause
  # (dcr-74c3158); anything needing more graduates instead. The
  # completions are held to the same bar, and the installed package
  # must carry them at the auto-linked paths.
  valley-cli = pkgs.runCommand "valley-cli" {
    nativeBuildInputs = [
      pkgs.git
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
  # and that the file and line it names are the ones it sat on. It goes
  # on to review a stack layer by layer, and a stale stack: approving its
  # top refused, its bottom approved, and review's [b]ase before the first
  # page, after which the pages and the request are at the rebased heads,
  # and the layer approved before the rebase is printed as a clean restack
  # instead of paged. Last, a note written beside an edited line of a
  # version paged as changed since its approval keeps its file and line.
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
  # --cost lists, and changes nothing required. So is the carry mode:
  # overlapping classes, and paths in different classes, report the
  # strictest of their modes, and an unclassified path is same-paths. A
  # mode written by refining `policy` counts. A pattern holding a newline
  # cannot forge a line of the render: the schema refuses it, the render
  # refuses it under a schema that admits it, and the deriver refuses the
  # second line under a render that admits it too.
  #
  # And the deriver fails closed: failNth stands in for git, sort and
  # cue, and fails each call the deriver makes in turn. Every failure
  # must end the derivation with nothing on stdout, because a failure
  # read as an empty list is a change that requires nothing. cliScript is
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
  # this flake's checks, and holds a docs-only change to requiring
  # prose-format alone. And it holds a check's own files to requiring
  # that check: every file in this directory named after a check, and every
  # other script here, read from the directory itself so a new one is
  # held as soon as it is added. It holds each scope to its carry mode,
  # the scopes the carry classes classify to every check they required
  # before, and the restacker's cap to the default.
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
  # the evidence and the request together. A change whose version names no
  # branch is approved at its recorded head the same way. Every check the scratch policy
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
      pkgs.jq # the timing log's lines
      pkgs.openssh
      packages.attest
      packages.valley-script
    ];
    schemaFile = ../../schema/verification.cue;
    cliScript = ../../bin/valley;
    inherit failNth;
  } (builtins.readFile ./valley-request.sh);

  # The rebase engine's tests (rebase/), run in its package's checkPhase
  # with go vet: a chain, a shared lower layer, a conflict, an empty layer,
  # a lease lost to a moved branch, a record append refused after the
  # branches moved, a branch push and a record push that origin takes and
  # never acknowledges, a cancellation before the push, and a cancellation
  # while a hook or a filter blocks, each over real repositories and real
  # pushes. A restack append kept and restored still reads the branch
  # before its push, and is refused once the owner has moved it; a kept
  # write that is malformed does not restore. valley-ready and
  # integrator-e2e drive it through valley rebase.
  rebase-unit = packages.rebase;

  # The Go verbs' tests (status/), run in valley-go's checkPhase: the
  # outcome records status accepts, the names its JSON refuses, its shell
  # quoting held to the bash it replaced, a rebase ended by a SIGTERM
  # while a hook blocks it, and the valley-checks/v1 document checks prints
  # over a real repository, a path that is not UTF-8 in base64. valley-status
  # and valley-ready drive the verbs through the CLI over real repositories.
  valley-go-unit = packages.valley-go;

  # Read origin's request/outcome refs and distinguish a pending request,
  # a recorded decision and a landing, without moving the user's checkout.
  # Each state is read again through --json, and jq holds the object to
  # the valley-status/v1 contract. A change's restack report is shown as
  # current, and as historical once a ref it observed has changed: its
  # branch, main, its record, a branch it saw missing, or a request that
  # held it back. Status, changes and pending all show it.
  valley-status = pkgs.runCommand "valley-status" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      packages.valley-script
    ];
  } (builtins.readFile ./valley-status.sh);

  # ready, unready and withdraw, through real pushes to a bare repo wired
  # with the rendered pre-receive hook, as each principal: a change created
  # at v1, a second version, the same head refused, a withdrawal, revived
  # at the same version and then at a new one, a principal that neither
  # owns the change nor holds the request grant refused by the hook, and
  # the operator, who holds it, let through. Then each source of a change's ID — the jj
  # header, the Change-Id trailer, a generated ID, --stamp — a header and a
  # trailer that disagree, and a change whose commits hold a merge. Last, a
  # change named by its handle is approved, reviewed and rebased at the head
  # its record names: a record pushed with its branch moved elsewhere in the
  # same push is refused, the branch at the recorded head is filed at that
  # head, and a later version whose branch is elsewhere is refused. A
  # version that names no branch is approved at its head, and refused by
  # review and rebase, which act on a branch. A name no record is on that
  # is no branch name is refused. Then a
  # draft: unready, ready again at the same head and at a new one, listed
  # apart by pending, refused by approve and rebase, and shown by review
  # as a draft, with neither [a]pprove, [b]ase nor [r]eject offered,
  # whether it is named by its handle or its branch, and a request pending
  # under either name warned of by unready and withdraw, a request for an
  # earlier version's branch too. review shows a withdrawn change the same
  # way. A review left open while another principal unreadies the change,
  # or readies a new version, refuses to act on it. A branch no record is
  # on is acted on as a branch, and one two records are on is refused.
  # Last, stacks rebased whole: every layer moved and each change given a
  # version, by ancestry and by trees; a
  # conflict in the middle layer; each refusal before the rebase; a branch
  # moved, and a plain branch readied, after the rebase read the stack; a
  # change unreadied between the branch push and its record write, and the
  # command the manifest gives to finish it; and a second stack left on an
  # old head. review of a stack whose middle layer is a draft, then
  # withdrawn, offers [a]pprove under it and neither at it nor above it.
  # Then review set against a change's approved version: a clean
  # restack after [b]ase, with the landing between the bases listed and no
  # diff paged; a later version with an edited file, paged with the
  # range-diff and its own diff of that file; a version whose message
  # alone changed and one whose author alone did, each flagged; a change
  # with no approved version, paged in full; and a stack whose lower layer
  # is set against its own approval while its top, which has none, is
  # paged. The hook is followed from the protected host's rendered init
  # script, so the rules the record meets are the ones a host applies.
  valley-ready = pkgs.runCommand "valley-ready" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      pkgs.util-linux # script(1), for approve's confirmation on a terminal
      packages.valley-script
    ];
    initScript = hosts.protectedHost.config.systemd.services.valley-init.script;
    passAsFile = [ "initScript" ];
  } (builtins.readFile ./valley-ready.sh);

  # valley changes and valley pending over the projects of one test host,
  # made by the integrator host's rendered valley-init, so discovery reads
  # the project list a host publishes and every record goes through the
  # hook it installs. Two repositories with ready, draft and withdrawn
  # records; handles unique across them; one listed repository that cannot
  # be read; a change ID recorded in several, with qualified handles; --repo;
  # a version landed by fast-forward, one landed as a new commit with a
  # transfer statement the integrator signed, one consumed with no
  # evidence, one whose statement another key signed, one with a landed
  # version's tree on another parent, a landing's tree replayed on a later
  # main, and heads below a merge that dropped them, through either
  # parent; a project named origin that shares a change ID; a
  # policy/valley git would read as an option or
  # as ext::; includes as the nearest ancestor only; pending's human shape,
  # its drafts array and --checks; and a branch name read as a branch again
  # once its change has landed, before the branch moves and after.
  valley-changes = pkgs.runCommand "valley-changes" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      pkgs.openssh
      packages.valley-script
      packages.valley-go
      packages.attest
    ];
    initScript = hosts.integratorHost.config.systemd.services.valley-init.script;
    passAsFile = [ "initScript" ];
    # The floor --checks derives under.
    floor = ../../examples/policy/instance;
  } (builtins.readFile ./valley-changes.sh);

  # valley knot against local bare repositories that run no hook, as any
  # git remote would: every verb, the ref's shape after each write, another
  # project named by --project and by a qualified id, and then each way a
  # write may fail to land the first time. A stand-in for git, first on
  # PATH, makes another writer land between a read and its push, drops the
  # connection after a push lands and after one that did not, takes a
  # claim or changes a field first, and releases and retakes a claim an
  # unclaim read. Each write must then land exactly once, or be reported
  # as a conflict. Two writers also run at once. cliScript
  # is bin/valley itself, because the package's wrapper puts its own git
  # ahead of the stand-in.
  valley-knot = pkgs.runCommand "valley-knot" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      packages.valley-go
    ];
    cliScript = ../../bin/valley;
  } (builtins.readFile ./valley-knot.sh);

  # valley plan against local bare repositories that run no hook: a graph
  # built with the real knot verbs, each at a time VALLEY_NOW fixes, so
  # every date and age it shows is the same on every run. The text, off a
  # terminal, and the valley-plan/v1 document are compared with golden
  # files kept beside the code that prints them (status/testdata/plan/).
  # Then a project with no graph, --all over the projects the instance
  # publishes with one that cannot be read, and the operator named by
  # --owner and by no one. cliScript is bin/valley itself, as for
  # valley-knot.
  valley-plan = pkgs.runCommand "valley-plan" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      pkgs.gawk
      pkgs.diffutils
      packages.valley-go
    ];
    cliScript = ../../bin/valley;
    golden = ../../status/testdata/plan;
  } (builtins.readFile ./valley-plan.sh);

  # Self-update's own tests (selfupdate/), run in its package's checkPhase
  # with go vet: a real bare repository for ls-remote and a stand-in for
  # nix, over an install, an up-to-date run, each step of the smoke test
  # failing and being skipped, --retry, a rollback and its return, a pin, a
  # valley removed from the profile by hand, and the repositories and
  # branches it refuses. valley-self-update drives the verb through the CLI.
  selfupdate-unit = packages.selfupdate;

  # valley --version and valley self-update through the installed package,
  # against a real bare repository. A build has no nix, so a stand-in for
  # nix builds a commit as a valley that runs the real, unstamped script
  # under the stamp nix would give that commit, beside the package's own
  # share/. The smoke test therefore runs the real CLI over the policy the
  # package ships, under the schema it carries. A run installs main's tip;
  # the next finds it up to date, also from a repository whose own git
  # config names another; a commit whose build cannot compose a policy
  # fails the smoke test, installs nothing, and is skipped. Then the
  # home-manager module, evaluated over stand-ins for the home-manager
  # options it sets: its timer, its service's PATH, and the arguments its
  # script runs valley with. A real nix build into a throwaway profile
  # needs a nix daemon and a writable store, which a build does not have.
  valley-self-update = pkgs.runCommand "valley-self-update" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      packages.valley
    ];
    inherit (packages.valley) version;
    valley = packages.valley;
    valleyScript = packages.valley-script;
    cliScript = ../../bin/valley;
    units = builtins.toJSON selfUpdateUnits;
    passAsFile = [ "units" ];
  } (builtins.readFile ./valley-self-update.sh);

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
