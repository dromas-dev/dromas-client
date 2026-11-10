package protocol

import (
	"maps"
	"net/http"
	"slices"
)

// Code is an API error's machine code, from the closed list below
// (PROTOCOL.md §11.3). A code names what a client does about the error: one
// call that can fail for causes a client meets differently has a code for
// each, and where the call itself tells them apart (a content_too_large on an ACL is
// entries, on a send bytes) one code serves.
type Code string

// The error codes, grouped by HTTP status. A code is snake_case, by the
// convention the standards share (OAuth's invalid_request, TLS's
// certificate_revoked, gRPC's NOT_FOUND and ALREADY_EXISTS): invalid_<thing>
// or unsupported_<thing> for an input, <thing>_<participle> for a state,
// <thing>_mismatch, <thing>_too_large or _too_low for a bound, and
// <action>_required; never an HTTP reason phrase. The test holds every code
// to it (TestCodeNames).
const (
	// 400
	CodeInvalidRequest             Code = "invalid_request"               // not JSON, a missing, unknown or ill-formed field (a byte field not base64 among them), a bad cursor, a send's body that is not an age file
	CodeInvalidAddress             Code = "invalid_address"               // not "d_" and 64 characters of base64url
	CodeIdentityMismatch           Code = "identity_mismatch"             // the identity does not hash to the address, or breaks its grammar
	CodeInvalidKey                 Code = "invalid_key"                   // an identity key, certificate or prekey statement that is malformed or does not verify
	CodePrekeyAlreadyUsed          Code = "prekey_already_used"           // the prekey was published before: every prekey is a new key
	CodeInvalidValidityPeriod      Code = "invalid_validity_period"       // the statement or its certificate is not valid at the server's clock: not begun, or ended
	CodeCertificateLifetimeTooLong Code = "certificate_lifetime_too_long" // the certificate runs longer than the server's maximum: max_certificate_lifetime_seconds
	CodeUnsupportedKeyType         Code = "unsupported_key_type"          // an identity of a key type this server does not register
	CodeInvalidRecipients          Code = "invalid_recipients"            // not exactly one stanza of a live prekey type
	CodeInvalidRevocation          Code = "invalid_revocation"            // not a revocation of the address: the document does not hash to it, or the signature is not its identity's own

	// 401, with WWW-Authenticate
	CodeInvalidPass           Code = "invalid_pass"           // no pass, or one that is not this mailbox's, for this server
	CodePassExpired           Code = "pass_expired"           // the pass, or the certificate it is under, is not valid at the server's clock: sign another, retry
	CodeCertificateSuperseded Code = "certificate_superseded" // the pass or statement is under a certificate that is neither the mailbox's current one nor above it
	CodeCertificateRevoked    Code = "certificate_revoked"    // a certificate the mailbox's identity revoked: which calls, PROTOCOL.md §8 and §11.4

	// 403
	CodeSenderNotRegistered      Code = "sender_not_registered"       // YOUR mailbox is not registered here, or was forgotten
	CodeSenderRevoked            Code = "sender_revoked"              // YOUR mailbox is revoked
	CodeSenderNotAllowed         Code = "sender_not_allowed"          // not on the mailbox's allow list, or on its block list
	CodeUnsupportedSenderKeyType Code = "unsupported_sender_key_type" // the mailbox's client does not verify identities of YOUR mailbox's key type
	CodeInvalidAdoptKey          Code = "invalid_adopt_key"           // an adopt key that is spent, expired, revoked or not this server's

	// 404
	CodeMailboxNotFound    Code = "mailbox_not_found"    // never registered, or forgotten
	CodeMessageNotFound    Code = "message_not_found"    // acked or expired: treat it as gone
	CodeRevocationNotFound Code = "revocation_not_found" // no revocation of this address is held here

	// 409
	CodeMailboxAlreadyExists   Code = "mailbox_already_exists"  // the address is registered with another prekey: a replay that is not byte-identical
	CodeSerialTooLow           Code = "serial_too_low"          // a prekey or a revocation of certificates at a serial not above the one held: the answer names it
	CodeConcurrentModification Code = "concurrent_modification" // the mailbox changed while the request was handled: send it again as it is
	CodePrekeySuperseded       Code = "prekey_superseded"       // a newer prekey exists: re-fetch, re-encrypt the same envelope
	CodePrekeyExpired          Code = "prekey_expired"          // the mailbox's owner must publish a new prekey

	// 408
	CodeRequestTimeout Code = "request_timeout" // the body did not arrive in the time the server waits for it: send it again

	// 410
	CodeMailboxRevoked           Code = "mailbox_revoked"            // the mailbox you named is revoked, forever
	CodeClientVersionUnsupported Code = "client_version_unsupported" // the server no longer takes the client's version: upgrade (PROTOCOL.md §11.17)

	// 412
	CodePreconditionFailed Code = "precondition_failed" // an If-Match the server evaluated false (RFC 9110 §13.1.1)

	// 413
	CodeContentTooLarge Code = "content_too_large"

	// 429, all with Retry-After
	CodeRateLimited        Code = "rate_limited"       // a limit takes no more for now: which one is quota_id, and whom it counted against subject
	CodeFetchRateLimited   Code = "fetch_rate_limited" // the mailbox has fetched all it may for now, new messages or again: the rest wait
	CodeMailboxFull        Code = "mailbox_full"
	CodeMailboxRateLimited Code = "mailbox_rate_limited" // the mailbox has taken all the new messages it takes for now

	// 503, with Retry-After
	CodeUnavailable Code = "unavailable"
)

var codes = map[Code]struct {
	status int
	title  string
}{
	CodeInvalidRequest:             {http.StatusBadRequest, "Invalid request"},
	CodeInvalidAddress:             {http.StatusBadRequest, "Invalid address"},
	CodeIdentityMismatch:           {http.StatusBadRequest, "Identity mismatch"},
	CodeInvalidKey:                 {http.StatusBadRequest, "Invalid key"},
	CodePrekeyAlreadyUsed:          {http.StatusBadRequest, "Prekey already used"},
	CodeInvalidValidityPeriod:      {http.StatusBadRequest, "Invalid validity period"},
	CodeCertificateLifetimeTooLong: {http.StatusBadRequest, "Certificate lifetime too long"},
	CodeUnsupportedKeyType:         {http.StatusBadRequest, "Unsupported key type"},
	CodeInvalidRecipients:          {http.StatusBadRequest, "Invalid recipients"},
	CodeInvalidRevocation:          {http.StatusBadRequest, "Invalid revocation"},
	CodeInvalidPass:                {http.StatusUnauthorized, "Invalid pass"},
	CodePassExpired:                {http.StatusUnauthorized, "Pass expired"},
	CodeCertificateSuperseded:      {http.StatusUnauthorized, "Certificate superseded"},
	CodeCertificateRevoked:         {http.StatusUnauthorized, "Certificate revoked"},
	CodeSenderNotRegistered:        {http.StatusForbidden, "Sender not registered"},
	CodeSenderRevoked:              {http.StatusForbidden, "Sender revoked"},
	CodeSenderNotAllowed:           {http.StatusForbidden, "Sender not allowed"},
	CodeUnsupportedSenderKeyType:   {http.StatusForbidden, "Unsupported sender key type"},
	CodeInvalidAdoptKey:            {http.StatusForbidden, "Invalid adopt key"},
	CodeMailboxNotFound:            {http.StatusNotFound, "Mailbox not found"},
	CodeMessageNotFound:            {http.StatusNotFound, "Message not found"},
	CodeRevocationNotFound:         {http.StatusNotFound, "Revocation not found"},
	CodeMailboxAlreadyExists:       {http.StatusConflict, "Mailbox already exists"},
	CodeSerialTooLow:               {http.StatusConflict, "Serial too low"},
	CodeConcurrentModification:     {http.StatusConflict, "Concurrent modification"},
	CodePrekeySuperseded:           {http.StatusConflict, "Prekey superseded"},
	CodePrekeyExpired:              {http.StatusConflict, "Prekey expired"},
	CodeRequestTimeout:             {http.StatusRequestTimeout, "Request timeout"},
	CodeMailboxRevoked:             {http.StatusGone, "Mailbox revoked"},
	CodeClientVersionUnsupported:   {http.StatusGone, "Client version unsupported"},
	CodePreconditionFailed:         {http.StatusPreconditionFailed, "Precondition failed"},
	CodeContentTooLarge:            {http.StatusRequestEntityTooLarge, "Content too large"},
	CodeRateLimited:                {http.StatusTooManyRequests, "Rate limited"},
	CodeFetchRateLimited:           {http.StatusTooManyRequests, "Fetch rate limited"},
	CodeMailboxFull:                {http.StatusTooManyRequests, "Mailbox full"},
	CodeMailboxRateLimited:         {http.StatusTooManyRequests, "Mailbox rate limited"},
	CodeUnavailable:                {http.StatusServiceUnavailable, "Unavailable"},
}

// Status returns the code's HTTP status, or 0 for a code this version does
// not know.
func (c Code) Status() int { return codes[c].status }

// Title returns the code's Problem Details title, or "" for a code this
// version does not know.
func (c Code) Title() string { return codes[c].title }

// Codes returns every code this version knows, sorted.
func Codes() []Code {
	return slices.Sorted(maps.Keys(codes))
}

// Known reports whether c is a code this version knows.
func (c Code) Known() bool { _, ok := codes[c]; return ok }

// TypePrefix begins every code's Problem Details "type": a tag URI (RFC
// 4151) that names the problem and resolves to nothing (PROTOCOL.md §11.3).
// The date is a whole day after dromas.dev was registered: RFC 4151 reads a
// month alone as its first day, before the registration. A tag is a name,
// never a location.
const TypePrefix = "tag:dromas.dev,2026-10-26:errors/"

// TypeURI returns the Problem Details "type" for the code.
func (c Code) TypeURI() string { return TypePrefix + string(c) }

// ProblemContentType is the media type of every error body (RFC 9457).
const ProblemContentType = "application/problem+json"

// Problem is an error body: RFC 9457 Problem Details, with the extension
// members code and hint, and one member for each value the error carries
// (PROTOCOL.md §11.3). Every value in detail is also a member of its own
// (AEP-193), so a client needs nothing from detail, hint or title: they are
// a server's words, for a person reading the body, and a client shows none
// of them. A client says what the code means in its own words, with the
// members it has checked to their types.
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int64  `json:"status"`
	Detail string `json:"detail,omitempty"`
	Code   Code   `json:"code"`
	Hint   string `json:"hint,omitempty"`

	// Limit is the limit hit by a content_too_large: the most bytes a body
	// or a ciphertext may hold, or entries an ACL.
	Limit *uint64 `json:"limit,omitempty"`
	// QuotaID is the limit a rate_limited names, and Subject whom it was
	// counted against (Google's QuotaFailure.Violation). A client shows each
	// only once it knows the value.
	QuotaID Quota   `json:"quota_id,omitempty"`
	Subject Subject `json:"subject,omitempty"`
	// QuotaValue is how many a day a rate_limited's limit takes, for one
	// counted by the day (QuotaSends, QuotaRenewals, QuotaCertificates,
	// QuotaCertificateRevocations): Google's QuotaFailure carries it as
	// quota_value beside quota_id.
	QuotaValue *uint64 `json:"quota_value,omitempty"`
	// PrekeySerial and CertificateSerial are serials of a prekey and of a
	// certificate: which ones each code names is PROTOCOL.md §11.3's.
	PrekeySerial      *uint64 `json:"prekey_serial,omitempty"`
	CertificateSerial *uint64 `json:"certificate_serial,omitempty"`
	// MaxCertificateLifetime is the longest a certificate the server takes may
	// run, valid-before less valid-after, in seconds (certificate_lifetime_too_long).
	MaxCertificateLifetime *uint64 `json:"max_certificate_lifetime_seconds,omitempty"`
	// Through is the revocation serial of the revocation of certificates
	// held: the serial it runs through (serial_too_low).
	Through *uint64 `json:"revocation_serial,omitempty"`
	// KeyTypes are the identity key types a server registers
	// (unsupported_key_type), and KeyType the one a mailbox's client does not
	// verify (unsupported_sender_key_type).
	KeyTypes []string `json:"key_types,omitempty"`
	KeyType  string   `json:"key_type,omitempty"`
	// Member is the request member or query parameter that is missing,
	// unknown, given twice or not of its form (invalid_request): Stripe's
	// param.
	Member string `json:"param,omitempty"`
	// MinVersion is the lowest version a server that retired the request's
	// takes, where it sets a floor (client_version_unsupported): a policy's version
	// (ParsePolicyVersion), which a client shows only once it has that form.
	MinVersion string `json:"min_version,omitempty"`
	// Record is the revocation record of the address a 410 mailbox_revoked names,
	// which the read of a mailbox always carries, and any other call's where its
	// server answers from the record (PROTOCOL.md §11.3). A client checks it,
	// and takes "revoked" on nothing less.
	Record *Revocations `json:"record,omitempty"`
}

// checkType holds a record a problem carries to the type it names, as one
// read alone is held.
func (p *Problem) checkType() error {
	if p.Record != nil {
		return p.Record.checkType()
	}
	return nil
}

// CodeAt returns the problem's code if it counts in an answer of the given
// status: a code this version knows, under its own status, in a body whose
// type is that code's own (PROTOCOL.md §11.3). Otherwise it returns "", and
// the answer is read by its status alone: a body that names its problem
// twice and disagrees names none.
func (p *Problem) CodeAt(status int) Code {
	if p.Code.Known() && p.Code.Status() == status && p.Type == p.Code.TypeURI() {
		return p.Code
	}
	return ""
}

// Quota is a limit a rate_limited names in quota_id, from the closed set
// below (PROTOCOL.md §11.5), as Google's QuotaFailure names one in its
// quota_id. Each is counted against one subject. A server's limit outside the
// set names none, and gives its subject alone.
type Quota string

// The quotas, by whom each is counted against.
const (
	QuotaPolls                  Quota = "polls"                   // a mailbox's polls
	QuotaPages                  Quota = "pages"                   // a mailbox's list pages after the first
	QuotaCalls                  Quota = "calls"                   // a mailbox's calls with its pass that no other limit counts
	QuotaSends                  Quota = "sends"                   // a mailbox's sends, a number a day
	QuotaRenewals               Quota = "renewals"                // a mailbox's renewals under its current certificate, a number a day
	QuotaCertificates           Quota = "certificates"            // its identity's new certificates, a number a day
	QuotaCertificateRevocations Quota = "certificate_revocations" // its identity's raises of its revocation of certificates, a number a day

	QuotaRegistrations        Quota = "registrations"           // registrations from an IP address: an IPv4 address or an IPv6 /64
	QuotaRegistrationsRange   Quota = "registrations_range"     // registrations from an IPv6 /48
	QuotaMisses               Quota = "misses"                  // calls from an IP address that named no mailbox or carried nothing that verified
	QuotaMissesRange          Quota = "misses_range"            // the same, from an IPv6 /48
	QuotaSendsBeforePass      Quota = "sends_before_pass"       // sends from an IP address, counted before their pass is read
	QuotaSendsBeforePassRange Quota = "sends_before_pass_range" // the same, from an IPv6 /48
	QuotaRequests             Quota = "requests"                // calls from an IP address that carry no pass the server has checked, registrations and renewals among them, and requests no route matches
	QuotaRequestsRange        Quota = "requests_range"          // the same, from an IPv6 /48
)

// Subject is whom a rate_limited was counted against (subject).
type Subject string

// The subjects.
const (
	SubjectMailbox Subject = "mailbox" // the mailbox the call proved itself to be, or whose identity signed it
	SubjectIP      Subject = "ip"      // the IP address the call came from, as the quota keys it
)

var quotas = map[Quota]Subject{
	QuotaPolls:                  SubjectMailbox,
	QuotaPages:                  SubjectMailbox,
	QuotaCalls:                  SubjectMailbox,
	QuotaSends:                  SubjectMailbox,
	QuotaRenewals:               SubjectMailbox,
	QuotaCertificates:           SubjectMailbox,
	QuotaCertificateRevocations: SubjectMailbox,
	QuotaRegistrations:          SubjectIP,
	QuotaRegistrationsRange:     SubjectIP,
	QuotaMisses:                 SubjectIP,
	QuotaMissesRange:            SubjectIP,
	QuotaSendsBeforePass:        SubjectIP,
	QuotaSendsBeforePassRange:   SubjectIP,
	QuotaRequests:               SubjectIP,
	QuotaRequestsRange:          SubjectIP,
}

// Known reports whether q is a quota this version knows.
func (q Quota) Known() bool { _, ok := quotas[q]; return ok }

// Subject returns whom q is counted against, or "" for a quota this version
// does not know.
func (q Quota) Subject() Subject { return quotas[q] }

// Quotas returns every quota this version knows, sorted.
func Quotas() []Quota { return slices.Sorted(maps.Keys(quotas)) }

// Known reports whether s is a subject this version knows.
func (s Subject) Known() bool { return s == SubjectMailbox || s == SubjectIP }
