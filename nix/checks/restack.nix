{
  pkgs,
  packages,
  hosts,
  ...
}:
{
  # The restacker planner's own tests (restack/), run in its checkPhase:
  # how a request relates to a family, which outcomes are current and
  # which release a family, the arithmetic of each capacity limit, and
  # how the policy's cap is counted, one larger than any int included.
  restack-unit = packages.restack-unwrapped;

  # valley-restacker once --report-only over a bare repository wired with
  # the protected host's rendered hook, every change readied by its owner
  # through the repository's own valley. Ready changes of two owners are
  # planned; a linear stack is one family, and so are two stacks on a
  # shared lower layer. A draft, a withdrawn change, a lone plain branch, a
  # change landed by fast-forward and an uncertain landing are left alone,
  # and a family with a plain branch or a draft in it is held. A change
  # with two versions uploaded by the principal is held by the project's
  # cap of 2. A request by name with no outcome holds; a current stale and
  # a current reject release, the reject noted; an outcome about another
  # head, tip or evidence, and one that does not read, hold. Requests by
  # exact head and by range hold, the range's head fetched by the planner,
  # and a request whose head is no commit holds every family. A cap larger
  # than any int composes and is no practical cap, so the change the cap
  # of 2 held is planned to restack. Every ref on origin is compared
  # before and after each pass. Outcomes are injected on the server side,
  # since no push may write one.
  restack-e2e = pkgs.runCommand "valley-restack-e2e" {
    nativeBuildInputs = [
      pkgs.git
      pkgs.jq
      packages.valley-script
      packages.valley-restacker
    ];
    initScript = hosts.protectedHost.config.systemd.services.valley-init.script;
    passAsFile = [ "initScript" ];
    smokePolicy = ../../examples/policy/smoke;
  } (builtins.readFile ./restack-e2e.sh);
}
