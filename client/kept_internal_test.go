package client

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
)

// RemoveInbox deletes the files it is named and, with each, the records of
// its handling, owed and given up, the handler's and the webhook's, and leaves
// every other message's alone (#1047). An id inbox/ does not hold refuses the
// whole call before anything is deleted.
func TestRemoveInboxDeletesTheFileAndItsHandlingRecords(t *testing.T) {
	t.Parallel()
	b, _, _ := twoOpens(t)
	ctx := context.Background()
	gone, kept, absent := fmt.Sprintf("%064x", 1), fmt.Sprintf("%064x", 2), fmt.Sprintf("%064x", 3)
	for _, id := range []string{gone, kept} {
		if err := writeFile(b.path("inbox", id+".json"), []byte("{}")); err != nil {
			t.Fatal(err)
		}
		owedAs(t, b, handlerRecords, id)
		owedAs(t, b, webhookRecords, id)
	}
	if err := b.lockedFinishedDo(ctx, func() error {
		return b.update(func(s store) error {
			for _, r := range []records{handlerRecords, webhookRecords} {
				for _, id := range []string{gone, kept} {
					if err := s.put(r.gaveUp, id, nil); err != nil {
						return err
					}
				}
			}
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}

	if ids, err := b.RemoveInbox(ctx, []string{gone, absent}); err == nil || len(ids) != 0 {
		t.Fatalf("RemoveInbox of an id inbox/ does not hold: %v, %v", ids, err)
	}
	if _, err := os.Stat(b.path("inbox", gone+".json")); err != nil {
		t.Fatalf("a refused call deleted a file: %v", err)
	}
	ids, err := b.RemoveInbox(ctx, []string{gone, gone})
	if err != nil || !slices.Equal(ids, []string{gone}) {
		t.Fatalf("RemoveInbox: %v, %v", ids, err)
	}
	if _, err := os.Stat(b.path("inbox", gone+".json")); !os.IsNotExist(err) {
		t.Fatalf("the file is still there: %v", err)
	}
	if _, err := os.Stat(b.path("inbox", kept+".json")); err != nil {
		t.Fatalf("another message's file went: %v", err)
	}
	if err := b.view(func(s store) error {
		for _, r := range []records{handlerRecords, webhookRecords} {
			var owed []string
			if err := s.each(r.owed, func(_, v []byte) error { owed = append(owed, string(v)); return nil }); err != nil {
				return err
			}
			if !slices.Equal(owed, []string{kept}) {
				t.Errorf("%s holds %v, want only the kept message", r.owed, owed)
			}
			var gaveUp []string
			if err := s.each(r.gaveUp, func(k, _ []byte) error { gaveUp = append(gaveUp, string(k)); return nil }); err != nil {
				return err
			}
			if !slices.Equal(gaveUp, []string{kept}) {
				t.Errorf("%s holds %v, want only the kept message", r.gaveUp, gaveUp)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
