package inventory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/inventory"
	"github.com/espeleo-katiuskas/katiuskas-intranet/backend/internal/testutil"
)

// createItem's rental/sale prices are set so tests can create a rental
// or sale movement without needing to give a price themselves — it's
// always taken from the item now (see Repository.CreateMovement).
func createItem(t *testing.T, tx pgx.Tx, repo *inventory.Repository, quantity int32) inventory.Item {
	t.Helper()
	description := "Casco Petzl Boreo"
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	rentalPrice, salePrice := 10.0, 25.0
	item, err := repo.CreateItem(context.Background(), inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, CategoryID: &categoryID,
		InitialQuantity: quantity, Loanable: true,
		RentalPricePerWeek: &rentalPrice, SalePrice: &salePrice,
	})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	return item
}

func TestCreateAndListItems(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)

	item := createItem(t, tx, repo, 3)
	if item.AvailableQuantity != 3 || item.TotalQuantity != 3 {
		t.Errorf("expected a freshly created item to start fully available, got %+v", item)
	}
	if item.Status != inventory.ItemAvailable {
		t.Errorf("expected default status available, got %q", item.Status)
	}

	list, err := repo.ListItems(context.Background())
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 item, got %d", len(list))
	}
}

func TestCreateItemRecordsInitialAcquisitionMovement(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 3)

	movements, err := repo.ListMovementsByItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("list movements: %v", err)
	}
	if len(movements) != 1 {
		t.Fatalf("expected 1 movement (the initial acquisition), got %d", len(movements))
	}
	if movements[0].Type != inventory.MovementAcquisition || movements[0].Quantity != 3 {
		t.Errorf("expected an acquisition of 3, got %+v", movements[0])
	}
	if movements[0].UserID != nil {
		t.Errorf("expected the initial acquisition to have no recipient, got %+v", movements[0].UserID)
	}
}

func TestLoanDecrementsAvailableQuantity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 3)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	m, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create movement: %v", err)
	}
	if m.Status != inventory.MovementCompleted {
		t.Errorf("expected a new loan to be completed immediately (only rental stays open), got %q", m.Status)
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].AvailableQuantity != 2 {
		t.Errorf("expected available quantity to drop to 2, got %d", items[0].AvailableQuantity)
	}
}

func TestLoanRejectedWhenInsufficientStock(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")

	_, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 2,
	})
	if !errors.Is(err, inventory.ErrInsufficientStock) {
		t.Errorf("expected ErrInsufficientStock, got %v", err)
	}
}

func TestSaleMarksItemSoldWhenStockReachesZero(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementSale, Quantity: 1,
	}); err != nil {
		t.Fatalf("create sale: %v", err)
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].AvailableQuantity != 0 || items[0].TotalQuantity != 0 {
		t.Errorf("expected total and available quantity 0, got %+v", items[0])
	}
	if items[0].Status != inventory.ItemSold {
		t.Errorf("expected status sold, got %q", items[0].Status)
	}
}

func TestLossMarksItemLostWhenStockReachesZero(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementLoss, Quantity: 1,
	}); err != nil {
		t.Fatalf("create loss: %v", err)
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].AvailableQuantity != 0 {
		t.Errorf("expected available quantity 0, got %d", items[0].AvailableQuantity)
	}
	if items[0].Status != inventory.ItemLost {
		t.Errorf("expected status lost, got %q", items[0].Status)
	}
}

func TestAbandonmentMarksItemAbandonedWhenStockReachesZero(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementAbandonment, Quantity: 1,
	}); err != nil {
		t.Fatalf("create abandonment: %v", err)
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].Status != inventory.ItemAbandoned {
		t.Errorf("expected status abandoned, got %q", items[0].Status)
	}
}

func TestRetirementMarksItemRetiredWhenStockReachesZero(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementRetirement, Quantity: 1,
	}); err != nil {
		t.Fatalf("create retirement: %v", err)
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].Status != inventory.ItemRetired {
		t.Errorf("expected status retired, got %q", items[0].Status)
	}
}

func TestAcquisitionIncreasesTotalAndAvailableQuantity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 3)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementAcquisition, Quantity: 2,
	}); err != nil {
		t.Fatalf("create acquisition: %v", err)
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].TotalQuantity != 5 || items[0].AvailableQuantity != 5 {
		t.Errorf("expected total and available quantity 5, got %+v", items[0])
	}
}

func TestAcquisitionRestocksAndClearsTerminalStatus(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementSale, Quantity: 1,
	}); err != nil {
		t.Fatalf("create sale: %v", err)
	}
	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].Status != inventory.ItemSold {
		t.Fatalf("expected status sold before restocking, got %q", items[0].Status)
	}

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementAcquisition, Quantity: 4,
	}); err != nil {
		t.Fatalf("create acquisition: %v", err)
	}

	items, err = repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].Status != inventory.ItemAvailable {
		t.Errorf("expected status back to available after restocking, got %q", items[0].Status)
	}
	if items[0].AvailableQuantity != 4 {
		t.Errorf("expected available quantity 4, got %d", items[0].AvailableQuantity)
	}
}

func TestAcquisitionRejectedForRopeAndBag(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Beal", "Antipodes", "C-200"
	ropeType := inventory.RopeTypeCaving
	diameter := 9.5
	year := int32(2024)
	length := int32(60)
	item, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, Brand: &brand, Model: &model, Code: &code,
		RopeType: &ropeType, DiameterMM: &diameter, ManufactureYear: &year, LengthM: &length,
		InitialQuantity: 1, Loanable: true,
	})
	if err != nil {
		t.Fatalf("create rope: %v", err)
	}

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementAcquisition, Quantity: 1,
	}); !errors.Is(err, inventory.ErrSingleUnitItem) {
		t.Errorf("expected ErrSingleUnitItem, got %v", err)
	}
}

func TestMovementsWithoutRecipientHaveNilUserLogin(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)

	m, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementLoss, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("create loss: %v", err)
	}
	if m.UserID != nil {
		t.Errorf("expected no recipient for a loss, got %+v", m.UserID)
	}

	list, err := repo.ListMovementsByStatus(ctx, inventory.MovementCompleted)
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	found := false
	for _, mv := range list {
		if mv.ID == m.ID {
			found = true
			if mv.UserLogin != nil {
				t.Errorf("expected nil user_login for a recipient-less movement, got %+v", mv.UserLogin)
			}
		}
	}
	if !found {
		t.Fatalf("expected the loss movement to appear in the completed listing")
	}
}

// The handler already rejects a login for these types before ever
// reaching the repository, but the recipient_only_for_loan_rental_sale
// CHECK (migration 00006_inventory.sql) is the actual source of truth —
// this exercises it directly.
func TestCreateMovementRejectsRecipientForAcquisition(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementAcquisition, Quantity: 1,
	}); err == nil {
		t.Error("expected an error giving a recipient to an acquisition")
	}
}

// Sale's recipient is optional — unlike rental, omitting it doesn't
// fail.
func TestCreateMovementSaleRecipientIsOptional(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)

	m, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementSale, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("create sale without a recipient: %v", err)
	}
	if m.UserID != nil {
		t.Errorf("expected no recipient, got %+v", m.UserID)
	}
}

// A loan's recipient is optional too — the activity it's tied to is
// the mandatory link, not necessarily one specific person.
func TestCreateMovementLoanRecipientIsOptional(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	activityID := testutil.InsertActivity(t, tx)

	m, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementLoan, Quantity: 1, ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan without a recipient: %v", err)
	}
	if m.UserID != nil {
		t.Errorf("expected no recipient, got %+v", m.UserID)
	}
}

func TestMovementNotesRoundTrip(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	notes := "cuerda gastada tras varios años de uso"

	m, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementRetirement, Quantity: 1, Notes: &notes,
	})
	if err != nil {
		t.Fatalf("create retirement: %v", err)
	}
	if m.Notes == nil || *m.Notes != notes {
		t.Errorf("expected notes to round-trip, got %+v", m.Notes)
	}
}

// A rental is the only type that stays 'active' until ReturnMovement
// closes it, all at once.
func TestReturnMovementRestoresStock(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 2)
	user := testutil.InsertUser(t, tx, "")

	rental, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementRental, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("create rental: %v", err)
	}

	returned, err := repo.ReturnMovement(ctx, rental.ID)
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if returned.Status != inventory.MovementCompleted {
		t.Errorf("expected status completed, got %q", returned.Status)
	}
	if returned.ReturnedAt == nil {
		t.Errorf("expected ReturnedAt to be set")
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].AvailableQuantity != 2 {
		t.Errorf("expected available quantity restored to 2, got %d", items[0].AvailableQuantity)
	}
	if items[0].Status != inventory.ItemAvailable {
		t.Errorf("expected status available again, got %q", items[0].Status)
	}
}

func TestReturnMovementNotFoundOrAlreadyClosed(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	if _, err := repo.ReturnMovement(ctx, 999999); !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("expected ErrNotFound for an unknown movement, got %v", err)
	}

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")
	rental, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementRental, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("create rental: %v", err)
	}
	if _, err := repo.ReturnMovement(ctx, rental.ID); err != nil {
		t.Fatalf("first return: %v", err)
	}
	if _, err := repo.ReturnMovement(ctx, rental.ID); !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("expected ErrNotFound returning an already-closed movement, got %v", err)
	}
}

// A loan (like every type other than rental) never reaches 'active' —
// see Repository.CreateMovement — so trying to return one behaves
// exactly like trying to return an unknown id: ErrNotFound. Its
// "return" is a separate 'return' movement instead (see
// TestReturnMovementValidatesAgainstOutstandingForActivity below).
func TestReturnMovementRefusesNonReturnableTypes(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	loan, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}

	if _, err := repo.ReturnMovement(ctx, loan.ID); !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListMovementsByItemAndStatus(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	}); err != nil {
		t.Fatalf("create loan: %v", err)
	}
	rental, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementRental, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create rental: %v", err)
	}

	byItem, err := repo.ListMovementsByItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("list by item: %v", err)
	}
	// The initial acquisition movement, plus the loan and the rental.
	if len(byItem) != 3 {
		t.Fatalf("expected 3 movements for this item, got %d", len(byItem))
	}

	// Only the rental reaches 'active' — the loan is completed the
	// moment it's recorded.
	active, err := repo.ListMovementsByStatus(ctx, inventory.MovementActive)
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(active) != 1 {
		t.Fatalf("expected 1 active movement (just the rental), got %d", len(active))
	}

	if _, err := repo.ReturnMovement(ctx, rental.ID); err != nil {
		t.Fatalf("return rental: %v", err)
	}
	active, err = repo.ListMovementsByStatus(ctx, inventory.MovementActive)
	if err != nil {
		t.Fatalf("list by status after return: %v", err)
	}
	if len(active) != 0 {
		t.Errorf("expected 0 active movements after returning the rental, got %d", len(active))
	}
}

// A 'return' isn't linked to any one specific loan row — it's
// validated against how much of this item that activity still has
// outstanding overall (every loan minus every return, for that item
// and activity together), matching the club's own example: lending 10
// then 15 more, then returning 20 followed by the remaining 5.
func TestReturnTypeValidatesAgainstOutstandingForActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 25)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	for _, qty := range []int32{10, 15} {
		if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
			ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: qty,
			ActivityID: &activityID,
		}); err != nil {
			t.Fatalf("create loan of %d: %v", qty, err)
		}
	}

	ret, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementReturn, Quantity: 20, ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create return of 20: %v", err)
	}
	if ret.Status != inventory.MovementCompleted {
		t.Errorf("expected a return to be completed immediately, got %q", ret.Status)
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	// 25 total, 5 still outstanding (25 loaned - 20 returned).
	if items[0].AvailableQuantity != 20 {
		t.Errorf("expected available quantity 20, got %d", items[0].AvailableQuantity)
	}
	if items[0].OutstandingLoanQuantity != 5 {
		t.Errorf("expected outstanding_loan_quantity 5, got %d", items[0].OutstandingLoanQuantity)
	}

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementReturn, Quantity: 5, ActivityID: &activityID,
	}); err != nil {
		t.Fatalf("create return of the remaining 5: %v", err)
	}
	items, err = repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].AvailableQuantity != 25 || items[0].OutstandingLoanQuantity != 0 {
		t.Errorf("expected everything back, got %+v", items[0])
	}
}

func TestReturnTypeRejectsExceedingOutstandingForActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 5,
		ActivityID: &activityID,
	}); err != nil {
		t.Fatalf("create loan: %v", err)
	}

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementReturn, Quantity: 6, ActivityID: &activityID,
	}); !errors.Is(err, inventory.ErrReturnExceedsLoaned) {
		t.Errorf("expected ErrReturnExceedsLoaned returning more than was loaned, got %v", err)
	}
}

// A return is scoped per activity — material loaned for one activity
// can't be "returned" against a different one, even if the same item
// has plenty outstanding overall.
func TestReturnTypeScopedPerActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 10)
	user := testutil.InsertUser(t, tx, "")
	activityA := testutil.InsertActivity(t, tx)
	activityB := testutil.InsertActivity(t, tx)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 5,
		ActivityID: &activityA,
	}); err != nil {
		t.Fatalf("create loan for activity A: %v", err)
	}

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementReturn, Quantity: 1, ActivityID: &activityB,
	}); !errors.Is(err, inventory.ErrReturnExceedsLoaned) {
		t.Errorf("expected ErrReturnExceedsLoaned returning against a different activity, got %v", err)
	}
}

func TestReturnTypeRequiresActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementReturn, Quantity: 1,
	}); !errors.Is(err, inventory.ErrActivityRequired) {
		t.Errorf("expected ErrActivityRequired for a return without an activity, got %v", err)
	}
}

// A return restocking a terminal-status item (sold/lost/abandoned/
// retired) clears its status back to available, the same as a further
// acquisition would.
func TestReturnTypeClearsTerminalItemStatus(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 2)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	// 1 unit out on loan, the other lost — draining available_quantity
	// to 0 marks the item lost, same as any other terminal movement.
	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	}); err != nil {
		t.Fatalf("create loan: %v", err)
	}
	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementLoss, Quantity: 1,
	}); err != nil {
		t.Fatalf("create loss: %v", err)
	}
	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].Status != inventory.ItemLost {
		t.Fatalf("expected status lost before the loaned unit comes back, got %q", items[0].Status)
	}

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, Type: inventory.MovementReturn, Quantity: 1, ActivityID: &activityID,
	}); err != nil {
		t.Fatalf("create return: %v", err)
	}

	items, err = repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].Status != inventory.ItemAvailable {
		t.Errorf("expected status back to available once the loaned unit is returned, got %q", items[0].Status)
	}
	if items[0].AvailableQuantity != 1 {
		t.Errorf("expected available quantity 1, got %d", items[0].AvailableQuantity)
	}
}

func TestListMovementsByUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	person := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &person.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	}); err != nil {
		t.Fatalf("create loan for person: %v", err)
	}
	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &other.ID, Type: inventory.MovementSale, Quantity: 1,
	}); err != nil {
		t.Fatalf("create sale for other: %v", err)
	}

	list, err := repo.ListMovementsByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 1 || list[0].UserID == nil || *list[0].UserID != person.ID {
		t.Fatalf("expected exactly this person's own movement, got %+v", list)
	}
}

func TestCreateMovementRequiresActivityForLoan(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
	}); !errors.Is(err, inventory.ErrActivityRequired) {
		t.Errorf("expected ErrActivityRequired for a loan without an activity, got %v", err)
	}
}

// A rental isn't always tied to one specific activity the way a loan
// always is.
func TestCreateMovementRentalDoesNotRequireActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementRental, Quantity: 1,
	}); err != nil {
		t.Errorf("expected a rental without an activity to succeed, got %v", err)
	}
}

func TestCreateMovementSaleDoesNotRequireActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")

	if _, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementSale, Quantity: 1,
	}); err != nil {
		t.Errorf("expected a sale without an activity to succeed, got %v", err)
	}
}

func TestCreateMovementStoresActivityID(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	m, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}
	if m.ActivityID == nil || *m.ActivityID != activityID {
		t.Errorf("expected activity id %d to round-trip, got %v", activityID, m.ActivityID)
	}
}

func TestCreateRopeItem(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Beal", "Antipodes", "C-042"
	ropeType := inventory.RopeTypeCaving
	diameter := 9.5
	year := int32(2024)
	length := int32(60)

	item, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, Brand: &brand, Model: &model, Code: &code,
		RopeType: &ropeType, DiameterMM: &diameter, ManufactureYear: &year, LengthM: &length,
		InitialQuantity: 1, Loanable: true,
	})
	if err != nil {
		t.Fatalf("create rope: %v", err)
	}
	if item.Type != inventory.ItemTypeRope || item.TotalQuantity != 1 || item.AvailableQuantity != 1 {
		t.Errorf("expected a single-unit rope item, got %+v", item)
	}
	if item.RopeType == nil || *item.RopeType != inventory.RopeTypeCaving {
		t.Errorf("expected rope_type caving, got %+v", item.RopeType)
	}
	if item.DiameterMM == nil || *item.DiameterMM != 9.5 {
		t.Errorf("expected diameter 9.5mm, got %+v", item.DiameterMM)
	}
	if item.LengthM == nil || *item.LengthM != 60 {
		t.Errorf("expected length 60m, got %+v", item.LengthM)
	}
	if item.CategoryID != nil {
		t.Errorf("expected no category for a rope, got %+v", item.CategoryID)
	}
}

func TestCreateBagItem(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Petzl", "Bulldozer", "S-017"
	bagType := inventory.BagTypeCaving
	capacity := 45.0

	item, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeBag, Brand: &brand, Model: &model, Code: &code,
		BagType: &bagType, CapacityLiters: &capacity, InitialQuantity: 1, Loanable: true,
	})
	if err != nil {
		t.Fatalf("create bag: %v", err)
	}
	if item.CapacityLiters == nil || *item.CapacityLiters != 45.0 {
		t.Errorf("expected capacity 45L, got %+v", item.CapacityLiters)
	}
	if item.BagType == nil || *item.BagType != inventory.BagTypeCaving {
		t.Errorf("expected bag_type caving, got %+v", item.BagType)
	}
}

// Each mismatched-fields scenario below gets its own transaction: a
// CHECK violation aborts the whole Postgres transaction, so a second
// query on the same tx (even an unrelated category lookup) would fail
// with "current transaction is aborted" instead of the outcome under
// test.

func TestCreateRopeRejectsMissingManufactureYear(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Beal", "Antipodes", "C-043"
	ropeType := inventory.RopeTypeCaving
	diameter := 9.5

	// Violates rope_requires_rope_fields.
	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, Brand: &brand, Model: &model, Code: &code,
		RopeType: &ropeType, DiameterMM: &diameter, InitialQuantity: 1, Loanable: true,
	}); err == nil {
		t.Error("expected an error for a rope missing manufacture_year")
	}
}

func TestCreateRopeRejectsMissingLengthM(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Beal", "Antipodes", "C-043b"
	ropeType := inventory.RopeTypeCaving
	diameter := 9.5
	year := int32(2024)

	// Violates rope_requires_rope_fields.
	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, Brand: &brand, Model: &model, Code: &code,
		RopeType: &ropeType, DiameterMM: &diameter, ManufactureYear: &year, InitialQuantity: 1, Loanable: true,
	}); err == nil {
		t.Error("expected an error for a rope missing length_m")
	}
}

func TestCreateGearRejectsMissingCategory(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	// Violates gear_requires_category.
	description := "Casco"
	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, InitialQuantity: 1, Loanable: true,
	}); err == nil {
		t.Error("expected an error for gear missing category_id")
	}
}

func TestCreateRopeRejectsMissingBrandModelCode(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	ropeType := inventory.RopeTypeCaving
	diameter := 9.5
	year := int32(2024)

	// Violates rope_and_bag_require_brand_model and
	// rope_and_bag_require_code.
	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, RopeType: &ropeType, DiameterMM: &diameter,
		ManufactureYear: &year, InitialQuantity: 1, Loanable: true,
	}); err == nil {
		t.Error("expected an error for a rope missing brand/model/code")
	}
}

func TestCreateBagRejectsMissingBagType(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Petzl", "Bulldozer", "S-018"
	capacity := 45.0

	// Violates bag_requires_bag_type.
	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeBag, Brand: &brand, Model: &model, Code: &code,
		CapacityLiters: &capacity, InitialQuantity: 1, Loanable: true,
	}); err == nil {
		t.Error("expected an error for a bag missing bag_type")
	}
}

func TestCreateGearRejectsMissingDescription(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	// Violates gear_requires_description.
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, CategoryID: &categoryID, InitialQuantity: 1, Loanable: true,
	}); err == nil {
		t.Error("expected an error for gear missing description")
	}
}

func TestCreateRopeRejectsInitialQuantityAboveOne(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Beal", "Antipodes", "C-044"
	ropeType := inventory.RopeTypeCaving
	diameter := 9.5
	year := int32(2024)
	length := int32(60)

	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, Brand: &brand, Model: &model, Code: &code,
		RopeType: &ropeType, DiameterMM: &diameter, ManufactureYear: &year, LengthM: &length,
		InitialQuantity: 3, Loanable: true,
	}); !errors.Is(err, inventory.ErrSingleUnitItem) {
		t.Errorf("expected ErrSingleUnitItem creating a rope with an initial quantity above 1, got %v", err)
	}
}

func TestItemRentableAndSellableAreComputedFromPrice(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	description := "Casco"
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	rentalPrice, salePrice := 5.0, 30.0

	withoutPrices, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, CategoryID: &categoryID,
		InitialQuantity: 1, Loanable: true,
	})
	if err != nil {
		t.Fatalf("create item without prices: %v", err)
	}
	if withoutPrices.Rentable || withoutPrices.Sellable {
		t.Errorf("expected neither rentable nor sellable with no prices set, got %+v", withoutPrices)
	}

	withPrices, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, CategoryID: &categoryID,
		InitialQuantity: 1, Loanable: true, RentalPricePerWeek: &rentalPrice, SalePrice: &salePrice,
	})
	if err != nil {
		t.Fatalf("create item with prices: %v", err)
	}
	if !withPrices.Rentable || !withPrices.Sellable {
		t.Errorf("expected both rentable and sellable once prices are set, got %+v", withPrices)
	}
}

func TestLoanRejectedWhenItemNotLoanable(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	description := "Casco"
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	item, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, CategoryID: &categoryID,
		InitialQuantity: 1, Loanable: false,
	})
	if err != nil {
		t.Fatalf("create non-loanable item: %v", err)
	}
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	_, err = repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if !errors.Is(err, inventory.ErrNotLoanable) {
		t.Errorf("expected ErrNotLoanable, got %v", err)
	}
}

// TestRentalRejectedWhenItemNotRentable and
// TestSaleRejectedWhenItemNotSellable mirror
// TestLoanRejectedWhenItemNotLoanable above for the two movement types
// that instead derive their own "can this even happen" flag from a
// price (Item.Rentable/Item.Sellable, see CreateItem) rather than a
// stored boolean — without this check, CreateMovement would have
// inserted the movement anyway with a nil price, hitting the database's
// own price_required_for_rental_and_sale CHECK constraint instead of
// answering with a clear, mapped error.
func TestRentalRejectedWhenItemNotRentable(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	description := "Casco"
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	item, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, CategoryID: &categoryID,
		InitialQuantity: 1, Loanable: true,
	})
	if err != nil {
		t.Fatalf("create item with no rental price: %v", err)
	}
	if item.Rentable {
		t.Fatalf("expected the item not to be rentable with no rental price set, got %+v", item)
	}
	user := testutil.InsertUser(t, tx, "")

	_, err = repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementRental, Quantity: 1,
	})
	if !errors.Is(err, inventory.ErrNotRentable) {
		t.Errorf("expected ErrNotRentable, got %v", err)
	}
}

func TestSaleRejectedWhenItemNotSellable(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	description := "Casco"
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	item, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, CategoryID: &categoryID,
		InitialQuantity: 1, Loanable: true,
	})
	if err != nil {
		t.Fatalf("create item with no sale price: %v", err)
	}
	if item.Sellable {
		t.Fatalf("expected the item not to be sellable with no sale price set, got %+v", item)
	}
	user := testutil.InsertUser(t, tx, "")

	_, err = repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementSale, Quantity: 1,
	})
	if !errors.Is(err, inventory.ErrNotSellable) {
		t.Errorf("expected ErrNotSellable, got %v", err)
	}
}

// TestCreateMovementNotFoundForNonexistentItem covers the fix for a
// previously wrong error: a movement against an item id that doesn't
// exist at all used to come back as ErrInsufficientStock ("not enough
// available quantity"), a confusing answer for an item that isn't
// there to have any quantity at all — a plain not-found, same as every
// other id-addressed lookup in this package.
func TestCreateMovementNotFoundForNonexistentItem(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	_, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: 999999, Type: inventory.MovementAcquisition, Quantity: 1,
	})
	if !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListMaterialCategories(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	categories, err := repo.ListMaterialCategories(ctx)
	if err != nil {
		t.Fatalf("list material categories: %v", err)
	}
	if len(categories) == 0 {
		t.Error("expected the seeded material categories to be listed")
	}
}

func TestMovementQuantityForcedToOneForRopeAndBag(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Beal", "Antipodes", "C-100"
	ropeType := inventory.RopeTypeCaving
	diameter := 9.5
	year := int32(2024)
	length := int32(60)

	item, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, Brand: &brand, Model: &model, Code: &code,
		RopeType: &ropeType, DiameterMM: &diameter, ManufactureYear: &year, LengthM: &length,
		InitialQuantity: 1, Loanable: true,
	})
	if err != nil {
		t.Fatalf("create rope: %v", err)
	}
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	// A rope only ever has 1 unit available, so asking for 5 would
	// already fail as insufficient stock even without the override —
	// what this actually checks is that the resulting movement's own
	// quantity is forced to 1, not whatever was requested.
	m, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 5,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}
	if m.Quantity != 1 {
		t.Errorf("expected quantity forced to 1 for a rope, got %d", m.Quantity)
	}
}

func TestMovementPriceIsAlwaysTakenFromTheItem(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	// createItem sets rental_price_per_week 10.0 and sale_price 25.0.
	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	rental, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementRental, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create rental: %v", err)
	}
	if rental.Price == nil || *rental.Price != 10.0 {
		t.Errorf("expected rental price 10.0 from the item, got %+v", rental.Price)
	}

	sale, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementSale, Quantity: 1,
	})
	if err != nil {
		t.Fatalf("create sale: %v", err)
	}
	if sale.Price == nil || *sale.Price != 25.0 {
		t.Errorf("expected sale price 25.0 from the item, got %+v", sale.Price)
	}

	loan, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}
	if loan.Price != nil {
		t.Errorf("expected a loan to always have a nil price, got %+v", loan.Price)
	}
}

func TestMovementDateDefaultsToTodayButCanBeBackdated(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	// The zero-value TransactedAt default lives in Service, not
	// Repository — exercise it through the service.
	service := inventory.NewService(repo)
	today, err := service.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create movement: %v", err)
	}
	if today.TransactedAt.Format("2006-01-02") != time.Now().Format("2006-01-02") {
		t.Errorf("expected transacted_at to default to today, got %v", today.TransactedAt)
	}

	backdated := time.Now().AddDate(0, 0, -5)
	past, err := repo.CreateMovement(ctx, inventory.CreateMovementInput{
		ItemID: item.ID, UserID: &user.ID, Type: inventory.MovementLoan, Quantity: 1, TransactedAt: backdated,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create backdated movement: %v", err)
	}
	if past.TransactedAt.Format("2006-01-02") != backdated.Format("2006-01-02") {
		t.Errorf("expected backdated transacted_at to round-trip, got %v want %v", past.TransactedAt, backdated)
	}
}
