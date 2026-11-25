package accounts_test

import (
	"context"
	"errors"
	"testing"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/access"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/accounts"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

func intranet(subject, name string) access.Viewer {
	return access.Viewer{Issuer: access.IntranetIssuer, Subject: subject, Name: name, Groups: []string{access.ClubGroup}}
}

// The first time a person acts they get an account, the same one every time after, and a new name from
// the issuer is kept.
func TestEnsureMakesOneAccountPerIdentity(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := accounts.NewService(accounts.NewRepository(tx))
	ctx := context.Background()

	first, err := svc.Ensure(ctx, intranet("subject-ana", "ana"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Kind != "person" || first.DisplayName != "ana" || first.ID == 0 {
		t.Errorf("a person's account: %+v", first)
	}
	again, err := svc.Ensure(ctx, intranet("subject-ana", "ana"))
	if err != nil || again.ID != first.ID {
		t.Errorf("the same person has the same account: %+v %v", again, err)
	}
	renamed, err := svc.Ensure(ctx, intranet("subject-ana", "ana.perez"))
	if err != nil || renamed.ID != first.ID || renamed.DisplayName != "ana.perez" {
		t.Errorf("a new name is kept on the same account: %+v %v", renamed, err)
	}
	other, err := svc.Ensure(ctx, intranet("subject-beto", "beto"))
	if err != nil || other.ID == first.ID {
		t.Errorf("another person has another account: %+v %v", other, err)
	}

	found, ok, err := svc.Of(ctx, access.IntranetIssuer, "subject-ana")
	if err != nil || !ok || found.ID != first.ID || found.DisplayName != "ana.perez" {
		t.Errorf("Of: %+v %v %v", found, ok, err)
	}
	if _, ok, err := svc.Of(ctx, access.IntranetIssuer, "nobody"); err != nil || ok {
		t.Errorf("a person who never acted has no account: %v %v", ok, err)
	}
}

// Who has no subject or no name has no account, and a service the catalogue does not know cannot vouch
// for anybody.
func TestEnsureRefusesWhoCannotHaveAnAccount(t *testing.T) {
	tx := testutil.NewTx(t)
	svc := accounts.NewService(accounts.NewRepository(tx))
	ctx := context.Background()
	for name, v := range map[string]access.Viewer{
		"anonymous":  access.Anonymous(),
		"no subject": {Issuer: access.IntranetIssuer, Name: "ana"},
		"no name":    {Issuer: access.IntranetIssuer, Subject: "s"},
	} {
		if _, err := svc.Ensure(ctx, v); !errors.Is(err, accounts.ErrNotSignedIn) {
			t.Errorf("%s: %v, want ErrNotSignedIn", name, err)
		}
	}
	if _, err := svc.Ensure(ctx, access.Viewer{Issuer: "somebody-else", Subject: "s", Name: "x"}); !errors.Is(err, accounts.ErrUnknownIssuer) {
		t.Errorf("an issuer that is not a client: %v, want ErrUnknownIssuer", err)
	}
}

// The intranet of the club is a service account, and the issuer it answers for.
func TestTheIntranetIsAServiceAccount(t *testing.T) {
	tx := testutil.NewTx(t)
	var kind, name string
	err := tx.QueryRow(context.Background(), `
		SELECT a.kind, a.display_name FROM catalogue.issuers i JOIN catalogue.accounts a ON a.id = i.account_id
		WHERE i.issuer = $1`, access.IntranetIssuer).Scan(&kind, &name)
	if err != nil || kind != "service" || name == "" {
		t.Errorf("the intranet: kind %q name %q err %v", kind, name, err)
	}
}
