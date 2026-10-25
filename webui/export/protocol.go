package export

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

// The upload's paths, under the receiver's base URL.
const (
	// ManifestPath takes step 1's POST of a signed manifest.
	ManifestPath = "/web-export/v1/manifest"
)

// BodyPath takes step 2's PUT of one body of the upload with sequence.
func BodyPath(sequence int64, sha256 string) string {
	return "/web-export/v1/" + strconv.FormatInt(sequence, 10) + "/" + sha256
}

// CommitPath takes step 3's POST that commits the upload with sequence.
func CommitPath(sequence int64) string {
	return "/web-export/v1/" + strconv.FormatInt(sequence, 10) + "/commit"
}

// The schemas of the receiver's replies.
const (
	InventorySchema = "valley-web-inventory/v1"
	CommittedSchema = "valley-web-committed/v1"
	ErrorSchema     = "valley-web-error/v1"
)

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
	// CodeTooLarge: a manifest over MaxManifest bytes, or a body declared
	// larger than the receiver's cap.
	CodeTooLarge Code = "too-large"
	// CodeEncoding: a body sent with a Content-Encoding.
	CodeEncoding Code = "content-encoding"
	// CodeUnreadable: a request body that could not be read.
	CodeUnreadable Code = "unreadable"
	// CodeBadSignature: a manifest that does not open under the pinned key.
	CodeBadSignature Code = "bad-signature"
	// CodeBadManifest: a manifest whose text is not a valid manifest.
	CodeBadManifest Code = "bad-manifest"
	// CodeWrongInstance and CodeWrongDestination: a manifest signed for
	// another instance, or for another receiver.
	CodeWrongInstance    Code = "wrong-instance"
	CodeWrongDestination Code = "wrong-destination"
	// CodeReplayed: a manifest whose sequence is not greater than the
	// greatest accepted. It is a replay of an accepted manifest, or older
	// than one. The publisher waits for its clock to pass HighWater.
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
)

// Status is the HTTP status a refusal is replied with.
func (c Code) Status() int {
	switch c {
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
