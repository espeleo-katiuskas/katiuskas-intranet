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

func createItem(t *testing.T, tx pgx.Tx, repo *inventory.Repository, quantity int32) inventory.Item {
	t.Helper()
	description := "Casco Petzl Boreo"
	categoryID := testutil.MaterialCategoryID(t, tx, "Mosquetones")
	item, err := repo.CreateItem(context.Background(), inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, CategoryID: &categoryID,
		TotalQuantity: quantity, Loanable: true,
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

func TestLoanDecrementsAvailableQuantity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 3)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)
	due := time.Now().AddDate(0, 0, 7)

	txn, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionLoan, Quantity: 1, DueDate: &due,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create transaction: %v", err)
	}
	if txn.Status != inventory.TransactionActive {
		t.Errorf("expected a new transaction to be active, got %q", txn.Status)
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

	_, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionLoan, Quantity: 2,
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
	price := 25.0

	if _, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionSale, Quantity: 1, Price: &price,
	}); err != nil {
		t.Fatalf("create sale: %v", err)
	}

	items, err := repo.ListItems(ctx)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	if items[0].AvailableQuantity != 0 {
		t.Errorf("expected available quantity 0, got %d", items[0].AvailableQuantity)
	}
	if items[0].Status != inventory.ItemSold {
		t.Errorf("expected status sold, got %q", items[0].Status)
	}
}

func TestReturnTransactionRestoresStock(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 2)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	loan, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}

	returned, err := repo.ReturnTransaction(ctx, loan.ID)
	if err != nil {
		t.Fatalf("return: %v", err)
	}
	if returned.Status != inventory.TransactionCompleted {
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

func TestReturnTransactionNotFoundOrAlreadyClosed(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	if _, err := repo.ReturnTransaction(ctx, 999999); !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("expected ErrNotFound for an unknown transaction, got %v", err)
	}

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)
	loan, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}
	if _, err := repo.ReturnTransaction(ctx, loan.ID); err != nil {
		t.Fatalf("first return: %v", err)
	}
	if _, err := repo.ReturnTransaction(ctx, loan.ID); !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("expected ErrNotFound returning an already-closed transaction, got %v", err)
	}
}

func TestReturnTransactionRefusesSales(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")
	price := 25.0

	sale, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionSale, Quantity: 1, Price: &price,
	})
	if err != nil {
		t.Fatalf("create sale: %v", err)
	}

	if _, err := repo.ReturnTransaction(ctx, sale.ID); !errors.Is(err, inventory.ErrCannotReturnSale) {
		t.Errorf("expected ErrCannotReturnSale, got %v", err)
	}
}

func TestListTransactionsByItemAndStatus(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	loan, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}
	price := 10.0
	if _, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionRental, Quantity: 1, Price: &price,
		ActivityID: &activityID,
	}); err != nil {
		t.Fatalf("create rental: %v", err)
	}

	byItem, err := repo.ListTransactionsByItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("list by item: %v", err)
	}
	if len(byItem) != 2 {
		t.Fatalf("expected 2 transactions for this item, got %d", len(byItem))
	}

	active, err := repo.ListTransactionsByStatus(ctx, inventory.TransactionActive)
	if err != nil {
		t.Fatalf("list by status: %v", err)
	}
	if len(active) != 2 {
		t.Fatalf("expected 2 active transactions, got %d", len(active))
	}

	if _, err := repo.ReturnTransaction(ctx, loan.ID); err != nil {
		t.Fatalf("return loan: %v", err)
	}
	active, err = repo.ListTransactionsByStatus(ctx, inventory.TransactionActive)
	if err != nil {
		t.Fatalf("list by status after return: %v", err)
	}
	if len(active) != 1 {
		t.Errorf("expected 1 active transaction after returning the loan, got %d", len(active))
	}
}

func TestListTransactionsByUser(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	person := testutil.InsertUser(t, tx, "")
	other := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	if _, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: person.ID, Type: inventory.TransactionLoan, Quantity: 1,
		ActivityID: &activityID,
	}); err != nil {
		t.Fatalf("create loan for person: %v", err)
	}
	price := 10.0
	if _, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: other.ID, Type: inventory.TransactionSale, Quantity: 1, Price: &price,
	}); err != nil {
		t.Fatalf("create sale for other: %v", err)
	}

	list, err := repo.ListTransactionsByUser(ctx, person.ID)
	if err != nil {
		t.Fatalf("list by user: %v", err)
	}
	if len(list) != 1 || list[0].UserID != person.ID {
		t.Fatalf("expected exactly this person's own transaction, got %+v", list)
	}
}

func TestCreateTransactionRequiresActivityForLoanAndRental(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")

	if _, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionLoan, Quantity: 1,
	}); !errors.Is(err, inventory.ErrActivityRequired) {
		t.Errorf("expected ErrActivityRequired for a loan without an activity, got %v", err)
	}
}

func TestCreateTransactionRentalRequiresActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")
	price := 10.0

	if _, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionRental, Quantity: 1, Price: &price,
	}); !errors.Is(err, inventory.ErrActivityRequired) {
		t.Errorf("expected ErrActivityRequired for a rental without an activity, got %v", err)
	}
}

func TestCreateTransactionSaleDoesNotRequireActivity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 1)
	user := testutil.InsertUser(t, tx, "")
	price := 25.0

	if _, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionSale, Quantity: 1, Price: &price,
	}); err != nil {
		t.Errorf("expected a sale without an activity to succeed, got %v", err)
	}
}

func TestCreateTransactionStoresActivityID(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	item := createItem(t, tx, repo, 5)
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	txn, err := repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if err != nil {
		t.Fatalf("create loan: %v", err)
	}
	if txn.ActivityID == nil || *txn.ActivityID != activityID {
		t.Errorf("expected activity id %d to round-trip, got %v", activityID, txn.ActivityID)
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

	item, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, Brand: &brand, Model: &model, Code: &code,
		RopeType: &ropeType, DiameterMM: &diameter, ManufactureYear: &year,
		TotalQuantity: 1, Loanable: true,
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
		BagType: &bagType, CapacityLiters: &capacity, TotalQuantity: 1, Loanable: true,
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
		RopeType: &ropeType, DiameterMM: &diameter, TotalQuantity: 1, Loanable: true,
	}); err == nil {
		t.Error("expected an error for a rope missing manufacture_year")
	}
}

func TestCreateGearRejectsMissingCategory(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	// Violates gear_requires_category.
	description := "Casco"
	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, TotalQuantity: 1, Loanable: true,
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
		ManufactureYear: &year, TotalQuantity: 1, Loanable: true,
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
		CapacityLiters: &capacity, TotalQuantity: 1, Loanable: true,
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
		Type: inventory.ItemTypeGear, CategoryID: &categoryID, TotalQuantity: 1, Loanable: true,
	}); err == nil {
		t.Error("expected an error for gear missing description")
	}
}

func TestCreateRopeAndBagAreForcedToSingleQuantity(t *testing.T) {
	tx := testutil.NewTx(t)
	repo := inventory.NewRepository(tx)
	ctx := context.Background()

	brand, model, code := "Beal", "Antipodes", "C-044"
	ropeType := inventory.RopeTypeCaving
	diameter := 9.5
	year := int32(2024)

	if _, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeRope, Brand: &brand, Model: &model, Code: &code,
		RopeType: &ropeType, DiameterMM: &diameter, ManufactureYear: &year,
		TotalQuantity: 3, Loanable: true,
	}); err == nil {
		t.Error("expected an error creating a rope with total_quantity > 1")
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
		TotalQuantity: 1, Loanable: true,
	})
	if err != nil {
		t.Fatalf("create item without prices: %v", err)
	}
	if withoutPrices.Rentable || withoutPrices.Sellable {
		t.Errorf("expected neither rentable nor sellable with no prices set, got %+v", withoutPrices)
	}

	withPrices, err := repo.CreateItem(ctx, inventory.CreateItemInput{
		Type: inventory.ItemTypeGear, Description: &description, CategoryID: &categoryID,
		TotalQuantity: 1, Loanable: true, RentalPricePerWeek: &rentalPrice, SalePrice: &salePrice,
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
		TotalQuantity: 1, Loanable: false,
	})
	if err != nil {
		t.Fatalf("create non-loanable item: %v", err)
	}
	user := testutil.InsertUser(t, tx, "")
	activityID := testutil.InsertActivity(t, tx)

	_, err = repo.CreateTransaction(ctx, inventory.CreateTransactionInput{
		ItemID: item.ID, UserID: user.ID, Type: inventory.TransactionLoan, Quantity: 1,
		ActivityID: &activityID,
	})
	if !errors.Is(err, inventory.ErrNotLoanable) {
		t.Errorf("expected ErrNotLoanable, got %v", err)
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
