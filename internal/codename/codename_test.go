package codename

import "testing"

// The old names each fail, so the check can fail; the new ones pass.
func TestCheck(t *testing.T) {
	for _, bad := range []string{"bad_request", "conflict", "unauthorized", "no_such_mailbox", "state_not_the_keys", "wrong_recipient_type", "stale", "replay", "revoked", "Mailbox_full", "a__b"} {
		if Check(bad) == nil {
			t.Errorf("%q passes", bad)
		}
	}
	for _, good := range []string{"invalid_request", "mailbox_already_exists", "serial_too_low", "message_not_yet_valid", "restore_required", "decrypt_error", "client_version_unsupported", "mailbox_full", "request_timeout", "prekey_already_used"} {
		if err := Check(good); err != nil {
			t.Error(err)
		}
	}
}
