package export

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// The receiver's paths, under its base URL. Every path is under
// PathPrefix. A receiver answers a path under LegacyPrefix, where the
// first version of the upload went, with an unsupported-version refusal.
const (
	PathPrefix   = "/web-export/v2/"
	LegacyPrefix = "/web-export/v1/"

	// AuthorizationPath takes the POST of a signed authorization.
	AuthorizationPath = PathPrefix + "authorization"
	// ManifestPath takes step 1's POST of a signed manifest.
	ManifestPath = PathPrefix + "manifest"
)

// BodyPath takes step 2's PUT of one body of the upload with sequence.
func BodyPath(sequence int64, sha256 string) string {
	return PathPrefix + strconv.FormatInt(sequence, 10) + "/" + sha256
}

// CommitPath takes step 3's POST that commits the upload with sequence.
func CommitPath(sequence int64) string {
	return PathPrefix + strconv.FormatInt(sequence, 10) + "/commit"
}

// The schemas of the receiver's replies.
const (
	AuthorizedSchema = "valley-web-authorized/v1"
	InventorySchema  = "valley-web-inventory/v2"
	CommittedSchema  = "valley-web-committed/v2"
	ErrorSchema      = "valley-web-error/v1"
)

// AuthorizationReply is the reply to an authorization the receiver admitted and
// stored durably: its sequence, which every request a server answers from
// then on is held to. It also says what content the receiver serves, so a
// publisher whose export is unchanged knows whether to upload it.
type AuthorizationReply struct {
	Schema   string `json:"schema"`
	Sequence int64  `json:"sequence"`
	// Active is the export the receiver serves, or nil for none.
	Active *Active `json:"active"`
	// Missing are the projects the active export holds and the stored
	// authorization names, whose repositories the receiver does not
	// hold: it deleted them when an earlier authorization withdrew the
	// project. A content upload sends their bundles again.
	Missing []string `json:"missing"`
}

// Serves reports whether the reply says the receiver serves the export
// with digest whole: it is the active export, and no repository of it
// that the authorization names is missing.
func (r *AuthorizationReply) Serves(digest string) bool {
	return r != nil && r.Active != nil && r.Active.Digest == digest && len(r.Missing) == 0
}

// Inventory is the reply to a manifest the receiver admitted: what it
// already holds, and the bodies it still needs before the upload can be
// committed.
type Inventory struct {
	Schema   string `json:"schema"`
	Sequence int64  `json:"sequence"`
	// Active is the export the receiver serves, or nil for none.
	Active *Active `json:"active"`
	// Holds are, per project, the source tip the receiver holds a verified
	// repository at.
	Holds []Held `json:"holds"`
	// Needs are the bodies the publisher must PUT, export.json first. A
	// bundle is not needed when the receiver holds its project at the
	// declared source tip, and no body is needed twice.
	Needs []Need `json:"needs"`
}

// Active is the export a receiver serves: its sequence and its content
// digest. A publisher whose export has this digest, and for which the
// receiver needs no bundle, need not commit.
type Active struct {
	Sequence int64  `json:"sequence"`
	Digest   string `json:"digest"`
}

// Held is a project the receiver holds a verified repository of, at its
// source tip.
type Held struct {
	Project   string `json:"project"`
	SourceTip string `json:"source_tip"`
}

// Need is one body the receiver needs, as the manifest declares it.
type Need struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Committed is the reply to a commit: the sequence now active.
type Committed struct {
	Schema   string `json:"schema"`
	Sequence int64  `json:"sequence"`
}

// ErrorReply is the reply to a request the receiver refused.
type ErrorReply struct {
	Schema  string `json:"schema"`
	Code    Code   `json:"error"`
	Message string `json:"message"`
	// HighWater is the greatest sequence accepted, given with replayed.
	HighWater int64 `json:"high_water,omitempty"`
	// Needs are the bodies still needed, given with incomplete.
	Needs []Need `json:"needs,omitempty"`
}

// Code is a refusal's stable name. A publisher acts on the code; the
// message is for people.
type Code string

const (
	// CodeUnauthenticated: a request that does not carry the receiver's
	// token.
	CodeUnauthenticated Code = "unauthenticated"
	// CodeTooLarge: a manifest over MaxManifest bytes, a body declared
	// larger than the receiver's cap, or bodies whose declared sizes
	// together pass the receiver's cap on one upload.
	CodeTooLarge Code = "too-large"
	// CodeEncoding: a body sent with a Content-Encoding.
	CodeEncoding Code = "content-encoding"
	// CodeUnreadable: a request body that could not be read.
	CodeUnreadable Code = "unreadable"
	// CodeBadSignature: a manifest that does not open under the pinned key.
	CodeBadSignature Code = "bad-signature"
	// CodeBadManifest: a manifest or authorization whose text is not a
	// valid one, or that is the other kind of document.
	CodeBadManifest Code = "bad-manifest"
	// CodeWrongInstance and CodeWrongDestination: a manifest signed for
	// another instance, or for another receiver.
	CodeWrongInstance    Code = "wrong-instance"
	CodeWrongDestination Code = "wrong-destination"
	// CodeReplayed: a manifest whose sequence is not greater than the
	// greatest accepted. It is a replay of an accepted manifest, or older
	// than one. The publisher waits for its clock to pass HighWater. For
	// an authorization, its sequence is not greater than the stored one's,
	// and the publisher signs the current policy again above HighWater.
	CodeReplayed Code = "replayed"
	// CodeOutOfWindow: a manifest signed more than MaxAge ago, or more than
	// MaxAhead ahead of the receiver's clock.
	CodeOutOfWindow Code = "out-of-window"
	// CodeSuperseded: a manifest older than the one staged, or a body or
	// commit for an upload a newer manifest replaced.
	CodeSuperseded Code = "superseded"
	// CodeNoManifest: a body or commit for a sequence nothing is staged
	// for.
	CodeNoManifest Code = "no-manifest"
	// CodeNotDeclared: a body whose hash the staged manifest does not
	// declare.
	CodeNotDeclared Code = "not-declared"
	// CodeSizeMismatch and CodeHashMismatch: a body whose size or SHA-256
	// is not what the manifest declares.
	CodeSizeMismatch Code = "size-mismatch"
	CodeHashMismatch Code = "hash-mismatch"
	// CodeBadExport: an export.json that is not a valid export, or that
	// disagrees with the manifest.
	CodeBadExport Code = "bad-export"
	// CodeBadBundle: a bundle the acceptance procedure refuses.
	CodeBadBundle Code = "bad-bundle"
	// CodeIncomplete: a commit before every needed body has verified.
	CodeIncomplete Code = "incomplete"
	// CodeUnsupportedVersion: a request under LegacyPrefix, from a
	// publisher older than the receiver's protocol.
	CodeUnsupportedVersion Code = "unsupported-version"
)

// Status is the HTTP status a refusal is replied with.
func (c Code) Status() int {
	switch c {
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	case CodeTooLarge:
		return http.StatusRequestEntityTooLarge
	case CodeEncoding:
		return http.StatusUnsupportedMediaType
	case CodeBadSignature, CodeWrongInstance, CodeWrongDestination, CodeOutOfWindow:
		return http.StatusForbidden
	case CodeReplayed, CodeSuperseded, CodeIncomplete:
		return http.StatusConflict
	case CodeNoManifest, CodeNotDeclared:
		return http.StatusNotFound
	case CodeHashMismatch, CodeBadExport, CodeBadBundle:
		return http.StatusUnprocessableEntity
	case CodeUnsupportedVersion:
		return http.StatusGone
	}
	return http.StatusBadRequest
}

// Error is a refusal: its code, and what was wrong.
type Error struct {
	Code    Code
	Message string
	// HighWater is set with CodeReplayed.
	HighWater int64
	// Needs is set with CodeIncomplete.
	Needs []Need
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Reply is the refusal as the receiver replies with it.
func (e *Error) Reply() ErrorReply {
	return ErrorReply{Schema: ErrorSchema, Code: e.Code, Message: e.Message, HighWater: e.HighWater, Needs: e.Needs}
}

// CodeOf is the code of the refusal err is or wraps, or "" for none.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func refuse(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
