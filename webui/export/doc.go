// Package export is the contract between the web UI's publisher, which
// runs on a valley host, and its receiver and server, which run on a web
// server the host pushes to and never hears back from. Both sides build
// against this package, so neither reinvents its rules.
//
// # The export
//
// An export is export.json and one git bundle of main per project. It
// holds only public projects, and only what an anonymous reader of one
// may see. Build projects the valley's own JSON onto it: `valley plan
// --all --json`, `valley pending --all --json`, and `valley knot show
// --project P <id> --json` for each knot of each public project, with
// each project's visibility, its main, and a display mapping for
// principal names. Visibility is per project: every knot and comment of
// an exported project is exported.
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
// visibility, graph tip, source tip and bundle. VerifyManifest admits one
// in a fixed order of steps, and CheckExport holds export.json to it.
//
// # The upload
//
// The publisher uploads an export to the receiver in three steps, each
// one HTTP request. Every reply is JSON.
//
//  1. POST ManifestPath with the signed manifest as the body. The
//     receiver admits it with VerifyManifest and stages it with
//     Store.Stage. It replies 200 with an Inventory: the export it
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
//   - step 1: too-large, bad-signature, bad-manifest, wrong-instance,
//     wrong-destination, replayed, out-of-window, in VerifyManifest's
//     order; superseded when a newer manifest is staged;
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
// # The receiver's state
//
// Store is the receiver's state directory: its generations, the active
// symlink, the high-water mark and the staging area. Open resolves the
// active generation once per request and holds it until released, and
// Activate makes a staged upload the active generation. The server and
// the receiver both use them.
package export
