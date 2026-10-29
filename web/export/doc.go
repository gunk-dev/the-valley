// Package export is the contract between the web UI's publisher, which
// runs on a valley host, and its receiver and server, which run on a web
// server the host pushes to and never hears back from. Both sides build
// against this package, so neither reinvents its rules.
//
// # The export
//
// An export is export.json and one git bundle of main per project. It
// holds the projects the publisher exports, and only what a reader of one
// may see. Build projects the valley's own JSON onto it: `valley plan
// --all --json`, `valley pending --all --json`, and `valley knot show
// --project P <id> --json` for each knot of each exported project, with
// each project's publication identity, its main, and a display mapping
// for principal names. Every knot and comment of an exported project is
// exported. The export says nothing of who may read a project.
//
// # The authorization
//
// An Authorization is a signed note, apart from the export, that names
// each project a reader may read, by name and publication identity, and
// its readers. Its sequence is the publisher's own counter, and the
// receiver admits one with VerifyAuthorization by its sequence alone,
// never its age. The receiver stores it with Store.Authorize, without the
// activation lock, and a server serves a project only while the stored
// authorization names it. No content manifest can grant or restore
// access.
//
// Digest is the export's content digest. It leaves out every
// display-time field, so the publisher uploads only when the valley
// changed, or when the receiver's inventory says it lacks something.
//
// # The manifest
//
// A Manifest is a signed note that names the instance that signed it,
// the destination it is for, its sequence (the signing time in Unix
// nanoseconds), export.json's hash and size, and each project's
// identity, graph tip, source tip and bundle. VerifyManifest admits one
// in a fixed order of steps, and CheckExport holds export.json to it.
//
// # The upload
//
// The publisher uploads an export to the receiver in three steps, each
// one HTTP request. Every reply is JSON.
//
// Every request carries the receiver's token, which ReadToken reads, in
// its Authorization header (Authorization). The token authenticates the
// transport; the manifest's signature still authenticates the export. The
// receiver refuses a request without the token as unauthenticated, with
// 401, before it reads the body or anything else of the request.
//
// Before the upload, the publisher sends its authorization: POST
// AuthorizationPath with the signed note as the body. The receiver admits
// it with VerifyAuthorization, stores it, and replies 200 with an
// AuthorizationReply, which says what export it serves.
//
//  1. POST ManifestPath with the signed manifest as the body. The
//     receiver admits it with VerifyManifest, holds the bodies it declares
//     to the receiver's caps, and stages it with Store.Stage. It replies
//     200 with an Inventory: the export it
//     serves, with its sequence and content digest; per project, the
//     source tip it holds a verified repository at; and the bodies it
//     still needs. Posting the same manifest again is harmless and gets
//     the same inventory, less what has been sent since. A publisher may
//     stop here when the inventory shows the receiver serves its digest
//     and needs no bundle; the next manifest it posts discards the
//     staged upload.
//  2. PUT BodyPath(sequence, sha256) for each needed body, export.json
//     and the bundles, with the body's bytes as they are: no
//     Content-Encoding, and a Content-Length, when given, equal to the
//     declared size. The receiver streams it into staging with
//     StageBody, and replies 204 once it verified. A body already staged
//     is accepted again without being read.
//  3. POST CommitPath(sequence) with an empty body. The receiver
//     activates the upload with Store.Activate only when every body has
//     verified, its bundles pass AcceptBundle, and export.json agrees
//     with the manifest. It replies 200 with Committed.
//
// A refusal is replied with Code.Status and an ErrorReply, whose error is
// a Code and stable. What each step refuses:
//
//   - every step: unauthenticated, before anything else;
//   - step 1: too-large, bad-signature, bad-manifest, wrong-instance,
//     wrong-destination, replayed, out-of-window, in VerifyManifest's
//     order; then too-large for a body over the receiver's cap, or bodies
//     over its cap on one upload together; superseded when a newer
//     manifest is staged;
//   - step 2: no-manifest when nothing is staged for the sequence,
//     superseded when a newer manifest replaced it, not-declared for a
//     hash the manifest does not declare, too-large, content-encoding,
//     size-mismatch, hash-mismatch;
//   - step 3: no-manifest, superseded, replayed, incomplete with the
//     bodies still needed, bad-export, bad-bundle.
//
// A replayed manifest, or one older than the receiver's high-water mark,
// is refused as replayed, with the high-water mark. The publisher's
// clock is behind the receiver's record, and it waits for its clock to
// pass the mark rather than publish out of order. A refused upload
// leaves the active export as it was.
//
// A commit has succeeded once the receiver has written its sequence to
// the high-water mark and swapped the active export to it, and the reply
// is then 200 with Committed. A later failure does not change the reply:
// deleting old exports, stale uploads and the trash, or syncing the swap
// to disk. Store passes it to the report function given to OpenStore,
// for the receiver to log, and the next sweep or OpenStore repairs it. A
// commit that fails before then leaves the active export as it was. If
// it failed after the high-water mark was written, its sequence is
// accepted, and the commit retried is refused as replayed. The
// publisher's next manifest, signed later, replaces the upload.
//
// # The receiver's state
//
// Store is the receiver's store: a descriptor file, and beside it the
// payload directory v2 with the authorization, the generations, the
// current symlink, the high-water mark and the staging area. Open
// resolves the current generation once per request and holds it until
// released, Activate makes a staged upload the current generation, and
// Prune deletes the repositories a withdrawal left. The receiver opens the
// store with OpenStore, and the server with OpenReadOnly, which needs only
// read access to it.
package export
