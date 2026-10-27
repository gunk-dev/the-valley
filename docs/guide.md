# A guide to working in the valley

This guide follows the jobs people do in a valley. Start with the [README](../README.md) for a short
tour, [Concepts and glossary](./concepts.md) for the terms used here, and
[Reference](./reference.md) for every command flag, environment variable, ref rule, and schema.

- [Client setup](#client-setup) gets an author or reviewer connected.
- [Authoring a change](#authoring-a-change) takes a local commit to review.
- [Reviewing and approving changes](#reviewing-and-approving-changes) takes a ready change to
  `main`.
- [Planning work with knots](#planning-work-with-knots) records and connects work.
- [Recovering from failure](#recovering-from-failure) explains what to do when work stops.
- [Running a valley host](#running-a-valley-host) sets up a NixOS deployment.
- [Contributing to the-valley](#contributing-to-the-valley) runs the project's development checks.

The commands below describe implemented behavior. Host names, principals, verification policy, and
service settings are deployment choices. Replace the example values with those your administrator
provides. The [README's current status](../README.md#configuration-and-current-status) lists what is
not built.

## Client setup

The client lets you propose work, review changes, and read the live plan. You need git, SSH access
to the valley host, and Nix with flakes enabled.

1. Clone the-valley's source from the URL your administrator provides. From that checkout, run:

   ```sh
   nix run .#valley -- help
   nix shell .#valley
   valley --version
   ```

   `nix run` runs one command. `nix shell` puts the packaged `valley` on your path for the rest of
   this guide. `valley help` prints a command summary; `valley help <verb>` prints help for one
   verb. The package supplies the helpers its commands need.

2. Clone the project you will work on and name the principal associated with your SSH key:

   ```sh
   git clone git@valley.example:demo
   cd demo
   git config valley.principal operator
   git config user.name "Your name"
   git config user.email you@example.com
   ```

   Use your own git author name and email. `valley.principal` names you in change records and knots.
   It does not grant permission. The host checks the principal associated with the key you actually
   use.

3. Check that the client can verify merged landings. You do not need to configure anything. The host
   publishes its trusted signers file and the integrator's signing name in the instance repository,
   and the client reads them when it needs them. This command shows the publication:

   ```sh
   git ls-remote git@valley.example:instance refs/the-valley/signers
   ```

   Use the instance repository your project's `policy/valley` names. If it prints nothing, the host
   publishes nothing, and some submitted versions appear `uncertain`; see
   [recovery](#an-uncertain-landing). To use other values than the host's, set them yourself. A
   setting wins over the publication:

   ```sh
   export VALLEY_KNOWN_SIGNERS="$HOME/.config/valley/known-signers"
   export VALLEY_INTEGRATOR_SIGNER=integrator
   ```

4. If approval runs checks on your machine, configure your Ed25519 attestation key and its
   registered signing name:

   ```sh
   export VALLEY_ATTEST_KEY="$HOME/.ssh/valley-attest"
   export VALLEY_ATTEST_NAME=operator
   ```

   When every required check already has eligible evidence on `origin` (for example, from a check
   worker), approval needs no signing key. When a check has to run locally, the host must trust that
   signing key. Your SSH principal must also be allowed to approve. These are separate parts of
   [identity](./concepts.md#principals-and-grants); see
   [Environment variables and configuration](./reference.md#environment-variables-and-configuration)
   for every client setting.

To install updates into your user Nix profile, name the-valley's source repository:

```sh
valley self-update --repo git@valley.example:the-valley --branch main
```

It builds the selected commit, tests the new client, and installs it. For repeated use, save the
repository with `git config --global valley.selfUpdate.repo git@valley.example:the-valley`, then run
`valley self-update`. Without a configured repository, self-update is off. Run
`valley self-update --help` for rollback and pinning.

The installed package includes Bash and Zsh completions. For a shell using the checkout directly,
source `completions/valley.bash` in Bash. In Zsh, add the checkout's `completions` directory to
`fpath` before running `autoload -Uz compinit; compinit`.

## Authoring a change

A change starts with a local commit. This example uses a new git branch called `greeting` to carry
it. Run it in a project with an initial `main` and verification policy already set up.

### Create and ready the first version

```sh
git fetch origin
git switch -c greeting origin/main
printf 'Hello from the valley.\n' > greeting.txt
git add greeting.txt
git commit -m 'Add a greeting'
valley checks
```

`valley checks` lists what this diff requires. It does not run the checks. Run the commands named by
your project's policy before asking for review. For a Nix check named `prose-format` in this
repository, for example:

```sh
nix build --no-link .#checks.x86_64-linux.prose-format -L
```

For this new, unpushed commit, stamp a stable Change ID and publish the first version:

```sh
valley ready --stamp
valley status greeting
```

`ready` prints a handle and version, such as `demo kqxm v1 ready`. Use the handle it actually prints
in later commands. The handle identifies the change even when its commit changes. `status` reports
readiness, any request, and the latest integration result. To see the required checks alongside
pending changes, run `valley pending --checks`.

If the commit already carries a `Change-Id` trailer or a jj `change-id` header, push it first and
run `valley ready` without `--stamp`:

```sh
git push -u origin HEAD
valley ready
```

Without `--stamp`, `ready` uses the pushed tip by default. Keep the full Change ID when recording a
new revision. The [change concept](./concepts.md#a-change-ready-approve-submitted) explains how
headers, trailers, and `--change <id>` preserve it.

### Revise or pause

Pause a ready change before editing it:

```sh
valley unready kqxm
```

It becomes a draft. If it has an approval, ask a principal allowed to approve to
[delete the outstanding request](#withdraw-or-revoke-an-approval) before you ready a replacement.
Otherwise, readying the edited change can let the old version land: its request still names the old
head.

Edit the files, then amend the commit while keeping its `Change-Id` trailer:

```sh
git add greeting.txt
git commit --amend --no-edit
git push --force-with-lease origin HEAD
valley ready
valley status kqxm
```

The new head becomes the next version of the same change. If you make a new commit without its
trailer or header, use `valley ready --change <full-change-id>` after pushing it.

### Withdraw or revoke an approval

To abandon the change, run `valley withdraw kqxm`. This marks it withdrawn. The integrator removes
outstanding requests on its next pass while the change is still withdrawn. Do not ready a withdrawn
change again until that removal is confirmed. For immediate revocation, a principal allowed to
approve can delete its request:

```sh
git push origin :refs/the-valley/integration-requests/main/c-<full-change-id>
```

Replace the whole `<full-change-id>` placeholder before running this command. If an older request
uses a branch name, delete that request too. Use
`git ls-remote origin 'refs/the-valley/integration-requests/main/*'` to list them. Neither pausing
nor withdrawing undoes a completed landing. See the
[integrator's final readiness check](./concepts.md#the-host-the-hook-and-the-integrator) for the
limit when a landing is already in progress.

### Restack onto main

Rebase a ready change when it needs the latest `main`. Before replacing an approved version,
[revoke its outstanding request](#withdraw-or-revoke-an-approval).

```sh
valley rebase kqxm
valley status kqxm
```

This rebases the whole stack beneath the named change, pushes the new heads, and records new
versions. It leaves your local checkout in place. Read its output before editing locally again;
fetch the remote updates and reconcile your local branch with them. If a record update fails, finish
with the exact `valley ready` command it prints.

Get the rebased version reviewed and approved again. A host that runs the restacker rebases ready
changes for you after each landing, and its versions need approval in the same way. Automatic
approval carry is not built. For conflicts or an uncertain landing, use
[recovery](#recovering-from-failure).

## Reviewing and approving changes

A principal allowed to approve can turn a ready change into an integration request. Start in a clone
of the project with the [client signing settings](#client-setup).

```sh
valley pending
valley review kqxm
```

Use a handle from `pending`. Review pages the diff and offers actions at the terminal. Choose
`[s]kip` to leave it alone or `[a]pprove` to run its required checks and approve the head you saw. A
draft or withdrawn layer stops review from offering approval for that layer or anything above it. A
stack is shown from the bottom up; approving its top includes the layers below.

For a revised change, review automatically compares it with an earlier approved version when one can
be identified. A clean restack gets a short summary. Use `[v]iew full diff` to see the whole change.
The [comparison rules](./architecture.md#review-against-the-approved-version) explain what can be
compared.

You can also approve from the command line:

```sh
valley approve kqxm
valley status kqxm
```

On a terminal, `approve` asks you to confirm the head and title. For automation, pass the exact
reviewed commit with `valley approve --head <sha> kqxm`. Approval reuses eligible evidence already
on `origin` for the commit's content tree (see [`valley approve`](./reference.md#valley-approve)),
runs and signs any remaining required checks, and publishes the new evidence and request together.
When every required check is covered, nothing runs and no signing key is needed. It does not wait
for landing. An enabled integrator processes the request; `status` tells you what happened.

After a successful landing, update your local `main`:

```sh
git fetch origin
git switch main
git merge --ff-only origin/main
```

To inspect all recorded changes, including submitted and withdrawn ones, use
`valley changes --json`. For changes across the instance, use `valley pending --all` or
`valley changes --json --all`; see [CLI JSON outputs](./reference.md#cli-json-outputs) for their
output schemas. These need access to the instance and project repositories.

### Watch activity and inspect results

`status` and `changes --json` read the records on the host. Use them to inspect landing results. If
your deployment provides access to its NATS bus, configure a NATS context for that endpoint and run:

```sh
valley tail
```

It streams events until you interrupt it. Bus access is a deployment choice; the default host bus
listens on loopback. An administrator can use `journalctl -u valley-integrator@demo` on the host for
service logs.

An administrator can republish the host's current refs with `replay`. On a host with
`services.valley.bus.enable = true` at its default address, enter `nix shell .#valley` from a
checkout of the-valley, then run:

```sh
sudo -u git env NATS_URL=nats://127.0.0.1:4222 "$(command -v valley)" replay /srv/git/demo.git
```

Use the deployment's bus address and credentials if they differ. This writes current-state events to
the bus; it is not a history viewer. See
[events and mirrors](./architecture.md#after-a-push-events-and-mirrors).

## Planning work with knots

The plan graph records work and the relationships between its knots. Read it from a clone of the
primary valley repository:

```sh
valley plan
valley plan --all
```

The first command shows this project's plan. The second reads projects published by the serving
host. A source mirror does not carry the plan graph. See
[project storage](./concepts.md#records-project-storage-the-plan-graph-and-knots).

Create an outcome and inspect the ID it prints:

```sh
valley knot new outcome 'Greeting reaches readers' --owner operator
valley knot list --status active
```

Use that ID in place of `oc-1234567` below:

```sh
valley knot show oc-1234567
valley knot set oc-1234567 status=in-progress
valley knot new bug 'Greeting needs an example' --parent oc-1234567
valley knot link oc-1234567 --change valley:demo/<full-change-id>
```

Replace `<full-change-id>` with the change's full ID. The link connects the plan to the work that
fulfills it. A dependency uses `--blocked-by <knot-id>` instead. When the outcome is complete:

```sh
valley knot set oc-1234567 status=done
valley plan
```

Comments record findings on a knot. `valley knot show` prints each comment's ID in brackets. A
comment's author or a holder of the `approve` grant can replace a comment's text with `--edit`:

```sh
valley knot comment oc-1234567 -m 'Waiting on the example.'
valley knot show oc-1234567
valley knot comment oc-1234567 --edit <comment-id> -m 'The example landed.'
```

The plan graph keeps every earlier text in its commit history. `valley knot log` lists the commits
that touched a knot or its comments, and `valley knot show --at <commit>` shows the knot at that
commit:

```sh
valley knot log oc-1234567
valley knot show oc-1234567 --at <commit>
```

Use `valley knot schema` to see each type's fields and allowed statuses. Knot writes validate the
resulting graph and push it directly; they do not go through change approval. See
[`valley knot`](./reference.md#valley-knot) for every subcommand and flag, and
[the plan graph](./architecture.md#the-plan-graph) for validation and concurrent edits.

## Recovering from failure

Start with `valley status <handle>`. It distinguishes work waiting for an integrator from a request
that the integrator has judged. Keep the command's full output when asking your administrator for
help.

### A stale or rejected request

A `stale` result means the change conflicts with `main` or required evidence is missing or does not
transfer across `main` movement. Read the reason in `status`. If the base moved,
[revoke the outstanding request](#withdraw-or-revoke-an-approval), restack the ready change, then
ask for a fresh review and approval:

```sh
valley rebase kqxm
valley status kqxm
```

For missing or expired evidence, the approver must rerun approval with trusted signing settings. If
approval reports that evidence from this signer already exists for the tree, follow its recovery
message; retrying the same head does not replace that evidence.

A `reject` result means the evidence is invalid, such as a signature or statement that does not
match what it claims. Keep the reason and ask the administrator to inspect the evidence and signing
configuration before trying again. See [checks and evidence](./concepts.md#checks-and-evidence).

If `valley rebase` reports a conflict, it pushes nothing. For a single change, start from its remote
head on a clean local branch, run `git fetch origin` and `git rebase origin/main`, resolve the
reported files, and use `git add <resolved-files>` followed by `git rebase --continue`. Use
`git rebase --abort` to abandon that local attempt. Push with `--force-with-lease`, then run
`valley ready --change <full-change-id>`. For a stack, resolve each layer in order; do not publish
only the top while lower change records still name old heads.

### A refused push

A refusal means the host did not accept that ref update. The message identifies the boundary:

- **Protected ref:** push the work to a topic branch and use readiness and approval. A deployment
  may seal `main` against every SSH principal.
- **Push hold:** ask the administrator to finish maintenance and remove the hold. Client settings
  cannot bypass it.
- **Record ownership or non-fast-forward record update:** fetch again and use `valley ready`,
  `unready`, or `withdraw` as the owner or a principal allowed to approve. Do not force a
  hand-edited record over someone else's update.
- **A ref named like another ref:** push the branch or tag under a name that does not begin with
  `refs/`.
- **Malformed or oversized record or knot:** read the named validation error. Use the CLI to make
  valid records and `valley knot schema` to inspect the knot model. Reduce oversized content before
  retrying.

The [push rules](./architecture.md#what-a-push-may-write) and
[ref layout table](./reference.md#project-storage-ref-layout-and-bounds) explain each boundary.

### An uncertain landing

An `uncertain` result means the client cannot prove whether the version was submitted. It can follow
a `failed` outcome or incomplete landing evidence. It can also mean the client had no signers to
verify the landing with. In that case the command says on stderr that a transfer statement could not
be verified, and why. Check that the host publishes its signers (step 3 of
[Client setup](#client-setup)). If it does not, ask the administrator for the signers file and the
integrator's signing name, set `VALLEY_KNOWN_SIGNERS` and `VALLEY_INTEGRATOR_SIGNER` to them, and
rerun `valley status <handle>`.

If it remains uncertain, ask the administrator to inspect `main`, the outcome record, and the
integrator logs together. Do not assume that a consumed request means a successful landing. Resolve
the uncertainty before rebasing or approving again. The
[landing definitions](./concepts.md#a-change-ready-approve-submitted) describe the evidence needed
to resolve this state.

## Running a valley host

A valley host serves repositories over SSH and runs the integrator on NixOS. This recipe creates an
instance repository named `instance` and a project named `demo`. It uses static keys and grants
first. All names, keys, checks, mirrors, and backup destinations are deployment choices; see
[NixOS host options](./reference.md#nixos-host-options) and
[CUE configuration schemas](./reference.md#cue-configuration-schemas) for the full option tables.

### Declare the host

In your NixOS configuration directory, create `valley.cue`:

```cue
package valley
projects: {
    instance: protection: {refs: ["refs/heads/main"], writers: []}
    demo: protection: {refs: ["refs/heads/main"], writers: []}
}
```

Both projects seal `main` against SSH pushes. The integrator writes locally on the host.
[`examples/hosts/protected.cue`](../examples/hosts/protected.cue) exercises four protection modes
for tests; it is not a production recipe.

To publish a project on the web, place it in a group. Declare the group under a permanent
identifier, and set `group` on the project to that identifier:

```cue
groups: demos: name: "demos"
projects: demo: group: "demos"
```

The web publisher exports only projects that belong to a group. The group's `name` is the first URL
segment and the project's web name is the second, giving `/demos/demo/…` here. A project's web name
defaults to its identifier unless the project sets `name`. When a group changes its `name`, keep the
old name in `aliases`. When a project changes its web name, keep the old name in `formerNames` so
existing links redirect (see [Groups and web URLs](./architecture.md#groups-and-web-urls)).

Add the-valley as an input named `valley` to your NixOS flake, using your administrator's source
URL. Import `valley.nixosModules.valley-host` in the host's modules. In a module that receives that
input, start with:

```nix
{ valley, ... }: {
  imports = [ valley.nixosModules.valley-host ];
  services.openssh.enable = true;
  services.valley = {
    enable = true;
    config = ./valley.cue;
    authorizedKeys = [{
      principal = "operator";
      key = "ssh-ed25519 REPLACE_WITH_OPERATOR_PUBLIC_KEY";
    }];
    grants.approve = [ "operator" ];
  };
}
```

Pass the input through `specialArgs = { inherit valley; };` in `nixpkgs.lib.nixosSystem` if using
this module's argument. Replace the key with the public SSH key that the operator uses to push. The
`approve` grant is the grant that lets a principal approve. It is also accepted as `request`, and
`grants.request` declares the same grant as `grants.approve`. Keep administrative SSH access
separate from this git account.

Apply your configuration using your normal NixOS deployment command. For a local flake with a host
named `valley-host`, run this on the host from that configuration directory:

```sh
sudo nixos-rebuild switch --flake .#valley-host
sudo systemctl start valley-init
sudo systemctl status valley-init --no-pager
```

`valley-init` creates `/srv/git/instance.git` and `/srv/git/demo.git`, installs hooks, and creates
no initial commit. It publishes the project list once `instanceProject` is configured. Keep the
integrator disabled until both repositories have a `main`.

### Seed the initial source

Create the initial source on the host in `/var/tmp/valley-seed`. Use an ordinary administrator
account with git's author name and email configured:

```sh
mkdir -p /var/tmp/valley-seed/instance/policy/instance
mkdir -p /var/tmp/valley-seed/demo/policy
cd /var/tmp/valley-seed/instance
git init --initial-branch=main
printf '# Instance\n' > README.md
cat > policy/instance/floor.cue <<'CUE'
package verification
floor: {
    checks: readme: {runner: "command", command: "test -s README.md", validity: "1h"}
    classes: source: {paths: {"**": true}, requires: {readme: true}}
    unclassified: readme: true
}
CUE
git add README.md policy
git commit -m 'Seed instance policy'
cd /var/tmp/valley-seed/demo
git init --initial-branch=main
printf '# Demo\n' > README.md
printf 'git@valley.example:instance\n' > policy/valley
git add README.md policy
git commit -m 'Seed demo'
```

Replace `valley.example` before committing. `policy/valley` names the instance repository whose
`policy/instance/` supplies this project's floor. The floor here requires only a nonempty README. It
is a smoke check to prove the approval path works. Choose checks that verify your actual projects
before admitting production work. A project's optional `policy/project/*.cue` can add requirements;
it cannot weaken the floor. See [policy](./architecture.md#policy).

Seed these commits through a local host write. The following commands are for the empty repositories
just created:

```sh
sudo chown -R git:git /var/tmp/valley-seed
sudo -u git git -C /srv/git/instance.git fetch /var/tmp/valley-seed/instance main:refs/heads/main
sudo -u git git -C /srv/git/demo.git fetch /var/tmp/valley-seed/demo main:refs/heads/main
```

Local writes bypass `pre-receive`, so this works while SSH writes to `main` are sealed. Keep these
seed directories free of private keys. Another bootstrap choice is to allow a temporary writer and
remove it after seeding; this recipe keeps the sealed configuration throughout.

### Provision signing and enable integration

On the approver's machine, create its attestation key if it does not have one. From the-valley's
checkout, export its public verifier entry:

```sh
ssh-keygen -t ed25519 -N '' -f "$HOME/.ssh/valley-attest"
nix run .#attest -- key --key "$HOME/.ssh/valley-attest" --name operator > operator.signer
```

Keep the private key on that machine. Copy only `operator.signer` to the administrator on the host.
On the host, create the integrator's separate key at a runtime path:

```sh
sudo install -d -m 0750 -o root -g git /var/lib/valley-secrets
sudo ssh-keygen -t ed25519 -N '' -f /var/lib/valley-secrets/integrator
sudo chgrp git /var/lib/valley-secrets/integrator
sudo chmod 0640 /var/lib/valley-secrets/integrator
```

On the host, clone the-valley's source from the URL your administrator provides. From that checkout,
enter `nix shell .#attest`. Export the integrator entry and combine the two public entries:

```sh
sudo "$(command -v attest)" key --key /var/lib/valley-secrets/integrator --name integrator > integrator.signer
cat operator.signer integrator.signer > known-signers
sudo install -m 0644 known-signers /var/lib/valley-secrets/known-signers
```

Add this to `services.valley` and apply the NixOS configuration again:

```nix
integrator = {
  enable = true;
  instanceProject = "instance";
  signingKeyFile = "/var/lib/valley-secrets/integrator";
  signingName = "integrator";
  knownSignersFile = "/var/lib/valley-secrets/known-signers";
};
```

The integrator runs as `valley-integrator` in the `git` group. Its key and signers file must be
readable by that account. Private keys belong at runtime paths, never in the Nix store.

The module requires a nonempty `authorizedKeys`, a signing key, a source for trusted signers, and
exactly one of `instanceProject` or `instancePolicy`. With `instanceProject`, it also requires the
instance's `main` to be protected. `instancePolicy` instead names a directory of floor CUE files on
a host that does not serve the instance repository. Set `signingName` explicitly to the name
published in the signers file.

The host publishes the `known-signers` file and the integrator's signing name for clients, at
`refs/the-valley/signers` in the instance repository. `valley-signers` writes it at boot, on
activation, and whenever the file changes, so clients need no signer settings. Follow
[Authoring a change](#authoring-a-change) in a clone of `demo`, then approve it as `operator`.
Verify that `valley status <handle>` reports the landing. On the host, inspect:

```sh
sudo systemctl status valley-integrator@demo --no-pager
sudo journalctl -u valley-integrator@demo -n 50 --no-pager
sudo journalctl -u valley-signers -n 20 --no-pager
```

### Use a governed identity registry

A deployment can maintain principals and keys in the instance source instead of listing all of them
in NixOS. Start with the [registry example](../examples/identity/registry/registry.cue) and
[schema](../schema/identity.cue). Write your own `identity/*.cue` in the instance repository, with
your public keys, signing names, boundaries, and grants. Do not deploy the example's keys.

Include a registry governor, the operator's push and approval grants, and a signing entry whose
`signs` value matches the integrator's `signingName`. Land the registry through the static setup
above. Then set `services.valley.identity.enable = true` and remove `integrator.knownSignersFile`
from the NixOS configuration. Apply it and inspect the compilation:

```sh
sudo systemctl start valley-identity
sudo journalctl -u valley-identity -n 50 --no-pager
```

It reads the integrated registry and writes `/var/lib/valley-identity/known-signers`, authorized
keys, and grants. Distribute that public signers file to clients. Keep a nonempty static
`authorizedKeys` list for bootstrap access. Static grants remain effective alongside compiled
grants; remove a static approval grant if the registry should be its sole source. See
[identity](./architecture.md#identity) for expiry and validation rules.

### Publish source mirrors

A mirror lets readers fetch the source without accessing the primary host. Add a mirror URL to the
project's declaration and apply the host configuration:

```cue
projects: demo: mirrors: ["git@mirror.example:team/demo.git"]
```

Provision the `git` Unix user's SSH private key and pinned `known_hosts` under `/srv/git/.ssh`. The
directory should be owned by `git` with mode `0700`; the private key should have mode `0600`. Verify
the mirror's host key through its administrator. The publisher runs as `git` and uses that account's
SSH configuration.

For an existing mirror, inspect its refs before cutover. Remove unwanted old refs explicitly; the
publisher does not clean up old topic branches or project storage. Start with an empty mirror when
possible. See [mirror cutover](./architecture.md#after-a-push-events-and-mirrors).

After a test landing, inspect the publisher:

```sh
sudo systemctl start valley-publish@demo
sudo journalctl -u valley-publish@demo -n 50 --no-pager
git ls-remote git@mirror.example:team/demo.git
```

Its `main` should match the primary. The publisher sends `main` and tags, not the plan graph or
other project storage. A mirror is not a backup of the valley.

### Back up and test a restore

Backups preserve the whole data directory. Add an offsite policy to `valley.cue`:

```cue
backup: {
    target: "restic-sftp"
    cadence: "nightly"
    retention: {daily: 7, weekly: 4, monthly: 6}
}
```

Provision four files outside the Nix store: the restic repository URL, its encryption password, the
SFTP private key, and pinned SSH host keys. Keep a recoverable copy of the password away from this
host. Point the module at those runtime files:

```nix
backup = {
  repositoryFile = "/var/lib/valley-secrets/restic-repository";
  passwordFile = "/var/lib/valley-secrets/restic-password";
  sshKeyFile = "/var/lib/valley-secrets/restic-ssh";
  knownHostsFile = "/var/lib/valley-secrets/restic-known-hosts";
};
```

After applying the configuration, take a backup and inspect its result:

```sh
sudo systemctl start restic-backups-valley
sudo journalctl -u restic-backups-valley -n 50 --no-pager
```

The service runs as root and snapshots `services.valley.dataDir`, `/srv/git` by default. Test a
restore into a separate empty directory. With `restic` available, run these in a root shell:

```sh
export RESTIC_REPOSITORY_FILE=/var/lib/valley-secrets/restic-repository
export RESTIC_PASSWORD_FILE=/var/lib/valley-secrets/restic-password
restic -o 'sftp.args=-i /var/lib/valley-secrets/restic-ssh -o UserKnownHostsFile=/var/lib/valley-secrets/restic-known-hosts -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes' snapshots
restic -o 'sftp.args=-i /var/lib/valley-secrets/restic-ssh -o UserKnownHostsFile=/var/lib/valley-secrets/restic-known-hosts -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes' restore latest --target /var/tmp/valley-restore
git -c safe.directory=/var/tmp/valley-restore/srv/git/demo.git -C /var/tmp/valley-restore/srv/git/demo.git fsck --full
git -c safe.directory=/var/tmp/valley-restore/srv/git/instance.git -C /var/tmp/valley-restore/srv/git/instance.git fsck --full
```

Expect a snapshot, a completed restore, and no corrupt or missing objects. Git may report dangling
objects; those alone do not indicate corruption. Inspect the restored `main` and `refs/the-valley/*`
as well. This verifies the restored repository, not a transaction across every ref written while the
backup ran.

### Hold pushes for maintenance

To refuse incoming pushes across every project, create the hold on the host:

```sh
sudo touch /srv/git/.valley-hold
```

Remove it after maintenance with `sudo rm /srv/git/.valley-hold`. Use your configured `dataDir` if
it differs. The hold stops SSH pushes; stop the relevant integrator services separately if
maintenance also requires local integration writes to stop.

### Find refs named like other refs

The push hook refuses to create or update a ref named like another ref: `refs/`, `refs/heads/`,
`refs/tags/` or `refs/remotes/` followed by a full ref name. When the ref it is named for is
missing, git reads, fetches and pushes to such a ref in its place. A host may hold some already,
made before the hook refused them. List them once, in every repository the host serves:

```sh
for repo in /srv/git/*.git; do
  sudo -u git git -C "$repo" for-each-ref --format="$repo %(refname)" \
    refs/refs/ refs/heads/refs/ refs/tags/refs/ refs/remotes/refs/
done
```

Each line names a repository and a ref. Once any work on a ref has been pushed under another name,
delete the ref on the host:

```sh
sudo -u git git -C /srv/git/demo.git update-ref -d refs/heads/refs/heads/main
```

Use your configured `dataDir` if it differs.

### Run the web edge

The web edge is a read-only web server for projects that `anyone` may read. The publisher on the
valley host pushes signed exports to the edge over HTTP, and the edge never connects back to the
valley host. On the edge, `valley-web-receive` receives uploads into a state directory and
`valley-web` serves them. They run as two processes under two Unix users over that shared state
directory (see [The receiver](./architecture.md#the-receiver)).

Generate a shared bearer token and install the same file on both hosts outside the Nix store,
readable by root alone:

```sh
head -c 32 /dev/urandom | base64 | sudo install -m 0400 /dev/stdin /run/keys/valley-web-receiver-token
```

On the valley host, enable the publisher with the receiver URL and token file:

```nix
imports = [ the-valley.nixosModules.default the-valley.nixosModules.web-publisher ];
services.valley.webPublisher = {
  enable = true;
  destination = "example.org";
  receiver = "https://example.org";
  receiverTokenFile = "/run/keys/valley-web-receiver-token";
};
```

Run the publisher once to generate the export signing key and read its verifier line:

```sh
sudo systemctl start valley-web-publish
sudo cat /var/lib/valley-web-publish/verifier
```

Until the receiver pins that verifier line, it refuses every upload with `bad-signature` and the
publisher pass fails.

On an edge host running NixOS, enable `services.valley.webReceiver`. Configure `listen` on the
address the publisher reaches and `webListen` behind the reverse proxy that serves the edge's public
domain:

```nix
imports = [ the-valley.nixosModules.web-receiver ];
services.valley.webReceiver = {
  enable = true;
  instance = "valley-host";
  destination = "example.org";
  verifiers = [ "valley-host/web-export+1a2b3c4d+AQ..." ];
  tokenFile = "/run/keys/valley-web-receiver-token";
  listen = "192.0.2.10:8081";
  webListen = "127.0.0.1:8080";
};
```

On an edge host that does not run NixOS, build and load the container image from a checkout of
the-valley:

```sh
nix build .#valley-web-image
docker load < result
```

`docker load` prints the image name and tag. Install the verifier line and a copy of the token where
the receiver's user (uid 990) can read them, then run two containers sharing one volume — writable
in `valley-web-receive` and read-only in `valley-web`:

```sh
sudo install -d -m 0750 -o 990 -g 990 /etc/valley-web
sudo install -m 0400 -o 990 -g 990 /run/keys/valley-web-receiver-token /etc/valley-web/token
echo 'valley-host/web-export+1a2b3c4d+AQ...' | sudo install -m 0444 -o 990 /dev/stdin /etc/valley-web/verifiers

docker run -d --name valley-web-receive --user valley-web-receive \
  --memory 2g --memory-swap 2g \
  -v valley-web:/var/lib/valley-web -v /etc/valley-web:/etc/valley-web:ro \
  -p 192.0.2.10:8081:8081 valley-web:<tag> \
  valley-web-receive --state /var/lib/valley-web --listen :8081 \
  --instance valley-host --destination example.org \
  --verifiers /etc/valley-web/verifiers --token-file /etc/valley-web/token

docker run -d --name valley-web --user valley-web \
  --memory 1g --memory-swap 1g \
  -v valley-web:/var/lib/valley-web:ro \
  -p 127.0.0.1:8080:8080 valley-web:<tag> \
  valley-web --state /var/lib/valley-web --listen :8080
```

A new volume inherits the owner and permissions of `/var/lib/valley-web` from the image: the
receiver's user with mode `0750`. `valley-web` reads the volume through the shared group.

Give each container its own memory limit. When a container reaches `--memory`, the kernel reclaims
memory or kills a process inside that container alone. Setting `--memory-swap` equal to `--memory`
disables swap for the container, matching the NixOS module's units. A limit keeps a spike in one
container from taking the other's memory or the host's only when the host has room for both limits
and for its own work. So keep the sum of both limits below the host's physical memory, with explicit
headroom left for the host's own work. The `2g` and `1g` limits above match the module's defaults
for `receiverMemoryMax` and `webMemoryMax`; sizing them for the edge's uploads and readers is a
deployment choice.

The receiver caps the state directory at `--max-storage` (8 GiB by default). Because `--max-storage`
is best effort, place the state directory on a fixed-size filesystem or under a disk quota slightly
larger than `--max-storage` (see [Bounds](./architecture.md#bounds)). For containers, apply that
bound to the filesystem backing the volume. On NixOS, mount a bounded filesystem at the receiver's
state directory (`/var/lib/valley-web` by default).

Because every upload request carries the token, reach the receiver over HTTPS, through a reverse
proxy that terminates TLS, or over a private network. On the next pass, the publisher prints
`the receiver activated sequence <n>` and `valley-web` serves the export.

## Contributing to the-valley

Contributors work in a checkout of this repository. Read [AGENTS.md](../AGENTS.md) before editing.
You can enter the default package's build environment:

```sh
nix develop
```

This flake defines no dedicated development shell. `nix develop` does not supply the full toolchain.
For the CLI and Go work, use an explicit tool shell from the repository root:

```sh
nix shell .#valley .#attest --inputs-from . nixpkgs#go nixpkgs#cue
```

Each Go component has its own module. Run fast tests from the component you changed, for example:

```sh
(cd status && go test ./...)
```

There is no root Go module. To test every Go module from the repository root:

```sh
for module in attest change composed delta identity integrator knot note rebase restack selfupdate sigverify status valleyhook; do
  (cd "$module" && go test ./...) || exit 1
done
```

In Nix, building a Go package does not run its unit tests. Those tests run only in the module's
`*-unit` check, such as `valleyhook-unit` for `valleyhook/` or `valley-go-unit` for `status/`:

```sh
nix build --no-link .#checks.x86_64-linux.valleyhook-unit -L
```

Run a single Nix check by name. For documentation, format first, then check formatting, links, and
the style rules, then the policy that selects checks:

```sh
nix run .#fmt
nix build --no-link .#checks.x86_64-linux.prose-format -L
nix build --no-link .#checks.x86_64-linux.own-policy -L
```

Stage new Markdown files before formatting so the formatter sees them. Review the diff afterward.
Use [nix/checks](../nix/checks/) to find the integration checks for the component you changed. Run
them one at a time; VM checks need the virtualization facilities their definitions specify. The full
flake suite is invoked with `nix flake check` when your task permits it and the machine can run all
its checks.

Each check is given only the files it reads. [nix/checks.nix](../nix/checks.nix) and
[nix/packages.nix](../nix/packages.nix) narrow each source to those files with `lib.fileset`. An
edit to any other file leaves the check's derivation unchanged, so the check's evidence transfers to
the edited tree. When a check starts to read a new file, give the check that file. The sandbox fails
a check that reads a file it was not given.

Editing a Go test changes only that module's `*-unit` check, not the other checks that use the
package. A Go package's source leaves out test files, and its `*-unit` check adds them back.

The NixOS host module uses three of the flake's programs: the integrator, the identity compiler, and
`valleyhook`. Four checks test what the module renders without running those programs:
`module-eval`, `mirror-e2e`, `bus-e2e`, and `init-e2e`. Those checks render the module with stand-in
scripts in place of the three programs ([nix/checks/hosts.nix](../nix/checks/hosts.nix)), so editing
one of the programs leaves those four checks unchanged. Checks that run one of the programs use the
real package.

To see which checks an edit gives a new derivation, run the measurement script on a commit:

```sh
nix/tools/drv-churn.sh --expect nix/tools/drv-churn.expected
```

It applies a fixed list of representative edits to the commit's tree, one at a time, and evaluates
every check's derivation after each. With `--expect`, it fails if any edit changes other checks than
the recorded table says. When a change means to alter the table, write the new one with `--write`.
With `--history origin/main~20..origin/main` instead, it replays the paths each of those commits
touched. Run `nix/tools/drv-churn.sh --help` for every option.

The [architecture](./architecture.md) explains component invariants and the checks that cover them.
The [reference](./reference.md) documents CLI flags, ref bounds, wire formats, JSON outputs, and
[schema](../schema/) definitions alongside the [concept glossary](./concepts.md#glossary).
