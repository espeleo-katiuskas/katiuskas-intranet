-- +goose Up
-- One rule for every foreign key: ON DELETE RESTRICT. Deleting something fails while anything still
-- references it; there is no cascade and no SET NULL, so nothing disappears that the request did not
-- name. Where an endpoint is meant to delete the parts of what it deletes (the entries of a
-- transaction, the orders of a distribution...) the code deletes them itself, in the same transaction.
-- (Until now some were CASCADE, two SET NULL and the rest the default, NO ACTION.)

ALTER TABLE accounts DROP CONSTRAINT accounts_parent_id_fkey;
ALTER TABLE accounts ADD CONSTRAINT accounts_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES accounts(id) ON DELETE RESTRICT;

ALTER TABLE activities DROP CONSTRAINT activities_leader_id_fkey;
ALTER TABLE activities ADD CONSTRAINT activities_leader_id_fkey FOREIGN KEY (leader_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE activity_participants DROP CONSTRAINT activity_participants_activity_id_fkey;
ALTER TABLE activity_participants ADD CONSTRAINT activity_participants_activity_id_fkey FOREIGN KEY (activity_id) REFERENCES activities(id) ON DELETE RESTRICT;

ALTER TABLE activity_participants DROP CONSTRAINT activity_participants_added_by_fkey;
ALTER TABLE activity_participants ADD CONSTRAINT activity_participants_added_by_fkey FOREIGN KEY (added_by) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE activity_participants DROP CONSTRAINT activity_participants_user_id_fkey;
ALTER TABLE activity_participants ADD CONSTRAINT activity_participants_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE activity_resources DROP CONSTRAINT activity_resources_activity_id_fkey;
ALTER TABLE activity_resources ADD CONSTRAINT activity_resources_activity_id_fkey FOREIGN KEY (activity_id) REFERENCES activities(id) ON DELETE RESTRICT;

ALTER TABLE activity_resources DROP CONSTRAINT activity_resources_author_id_fkey;
ALTER TABLE activity_resources ADD CONSTRAINT activity_resources_author_id_fkey FOREIGN KEY (author_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE board_members DROP CONSTRAINT board_members_user_id_fkey;
ALTER TABLE board_members ADD CONSTRAINT board_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE budget_item_accounts DROP CONSTRAINT budget_item_accounts_account_id_fkey;
ALTER TABLE budget_item_accounts ADD CONSTRAINT budget_item_accounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE RESTRICT;

ALTER TABLE budget_item_accounts DROP CONSTRAINT budget_item_accounts_item_id_budget_id_fkey;
ALTER TABLE budget_item_accounts ADD CONSTRAINT budget_item_accounts_item_id_budget_id_fkey FOREIGN KEY (item_id, budget_id) REFERENCES budget_items(id, budget_id) ON DELETE RESTRICT;

ALTER TABLE budget_items DROP CONSTRAINT budget_items_budget_id_fkey;
ALTER TABLE budget_items ADD CONSTRAINT budget_items_budget_id_fkey FOREIGN KEY (budget_id) REFERENCES budgets(id) ON DELETE RESTRICT;

ALTER TABLE distribution_articles DROP CONSTRAINT distribution_articles_distribution_id_fkey;
ALTER TABLE distribution_articles ADD CONSTRAINT distribution_articles_distribution_id_fkey FOREIGN KEY (distribution_id) REFERENCES distributions(id) ON DELETE RESTRICT;

ALTER TABLE distribution_options DROP CONSTRAINT distribution_options_distribution_id_fkey;
ALTER TABLE distribution_options ADD CONSTRAINT distribution_options_distribution_id_fkey FOREIGN KEY (distribution_id) REFERENCES distributions(id) ON DELETE RESTRICT;

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_beneficiary_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_beneficiary_id_fkey FOREIGN KEY (beneficiary_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_distribution_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_distribution_id_fkey FOREIGN KEY (distribution_id) REFERENCES distributions(id) ON DELETE RESTRICT;

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_recorded_by_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_recorded_by_id_fkey FOREIGN KEY (recorded_by_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_transaction_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE RESTRICT;

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_unit_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_unit_id_fkey FOREIGN KEY (unit_id) REFERENCES distribution_units(id) ON DELETE RESTRICT;

ALTER TABLE distribution_unit_identifiers DROP CONSTRAINT distribution_unit_identifiers_article_id_fkey;
ALTER TABLE distribution_unit_identifiers ADD CONSTRAINT distribution_unit_identifiers_article_id_fkey FOREIGN KEY (article_id) REFERENCES distribution_articles(id) ON DELETE RESTRICT;

ALTER TABLE distribution_unit_identifiers DROP CONSTRAINT distribution_unit_identifiers_paid_marked_by_id_fkey;
ALTER TABLE distribution_unit_identifiers ADD CONSTRAINT distribution_unit_identifiers_paid_marked_by_id_fkey FOREIGN KEY (paid_marked_by_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE distribution_unit_identifiers DROP CONSTRAINT distribution_unit_identifiers_unit_id_fkey;
ALTER TABLE distribution_unit_identifiers ADD CONSTRAINT distribution_unit_identifiers_unit_id_fkey FOREIGN KEY (unit_id) REFERENCES distribution_units(id) ON DELETE RESTRICT;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_article_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_article_id_fkey FOREIGN KEY (article_id) REFERENCES distribution_articles(id) ON DELETE RESTRICT;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_beneficiary_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_beneficiary_id_fkey FOREIGN KEY (beneficiary_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_distribution_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_distribution_id_fkey FOREIGN KEY (distribution_id) REFERENCES distributions(id) ON DELETE RESTRICT;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_paid_marked_by_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_paid_marked_by_id_fkey FOREIGN KEY (paid_marked_by_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_picked_up_by_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_picked_up_by_id_fkey FOREIGN KEY (picked_up_by_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_pickup_recorded_by_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_pickup_recorded_by_id_fkey FOREIGN KEY (pickup_recorded_by_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_registered_by_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_registered_by_id_fkey FOREIGN KEY (registered_by_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE distributions DROP CONSTRAINT distributions_account_id_fkey;
ALTER TABLE distributions ADD CONSTRAINT distributions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE RESTRICT;

ALTER TABLE distributions DROP CONSTRAINT distributions_created_by_fkey;
ALTER TABLE distributions ADD CONSTRAINT distributions_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE licenses DROP CONSTRAINT licenses_license_type_id_fkey;
ALTER TABLE licenses ADD CONSTRAINT licenses_license_type_id_fkey FOREIGN KEY (license_type_id) REFERENCES license_types(id) ON DELETE RESTRICT;

ALTER TABLE licenses DROP CONSTRAINT licenses_transaction_id_fkey;
ALTER TABLE licenses ADD CONSTRAINT licenses_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE RESTRICT;

ALTER TABLE licenses DROP CONSTRAINT licenses_user_id_fkey;
ALTER TABLE licenses ADD CONSTRAINT licenses_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE magic_link_tokens DROP CONSTRAINT magic_link_tokens_user_id_fkey;
ALTER TABLE magic_link_tokens ADD CONSTRAINT magic_link_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE material_items DROP CONSTRAINT material_items_type_fkey;
ALTER TABLE material_items ADD CONSTRAINT material_items_type_fkey FOREIGN KEY (type_id, kind) REFERENCES material_types(id, kind) ON DELETE RESTRICT;

ALTER TABLE material_movements DROP CONSTRAINT material_movements_activity_id_fkey;
ALTER TABLE material_movements ADD CONSTRAINT material_movements_activity_id_fkey FOREIGN KEY (activity_id) REFERENCES activities(id) ON DELETE RESTRICT;

ALTER TABLE material_movements DROP CONSTRAINT material_movements_item_id_fkey;
ALTER TABLE material_movements ADD CONSTRAINT material_movements_item_id_fkey FOREIGN KEY (item_id) REFERENCES material_items(id) ON DELETE RESTRICT;

ALTER TABLE material_movements DROP CONSTRAINT material_movements_recorded_by_fkey;
ALTER TABLE material_movements ADD CONSTRAINT material_movements_recorded_by_fkey FOREIGN KEY (recorded_by) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE material_movements DROP CONSTRAINT material_movements_user_id_fkey;
ALTER TABLE material_movements ADD CONSTRAINT material_movements_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE membership_dues DROP CONSTRAINT membership_dues_transaction_id_fkey;
ALTER TABLE membership_dues ADD CONSTRAINT membership_dues_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE RESTRICT;

ALTER TABLE membership_dues DROP CONSTRAINT membership_dues_user_id_fkey;
ALTER TABLE membership_dues ADD CONSTRAINT membership_dues_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE membership_leaves DROP CONSTRAINT membership_leaves_user_id_fkey;
ALTER TABLE membership_leaves ADD CONSTRAINT membership_leaves_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE memberships DROP CONSTRAINT memberships_user_id_fkey;
ALTER TABLE memberships ADD CONSTRAINT memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE tokens DROP CONSTRAINT tokens_impersonated_by_fkey;
ALTER TABLE tokens ADD CONSTRAINT tokens_impersonated_by_fkey FOREIGN KEY (impersonated_by) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE tokens DROP CONSTRAINT tokens_user_id_fkey;
ALTER TABLE tokens ADD CONSTRAINT tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE transaction_entries DROP CONSTRAINT transaction_entries_account_id_fkey;
ALTER TABLE transaction_entries ADD CONSTRAINT transaction_entries_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE RESTRICT;

ALTER TABLE transaction_entries DROP CONSTRAINT transaction_entries_transaction_id_fkey;
ALTER TABLE transaction_entries ADD CONSTRAINT transaction_entries_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE RESTRICT;

ALTER TABLE transaction_files DROP CONSTRAINT transaction_files_transaction_id_fkey;
ALTER TABLE transaction_files ADD CONSTRAINT transaction_files_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE RESTRICT;

ALTER TABLE transaction_files DROP CONSTRAINT transaction_files_uploaded_by_fkey;
ALTER TABLE transaction_files ADD CONSTRAINT transaction_files_uploaded_by_fkey FOREIGN KEY (uploaded_by) REFERENCES users(id) ON DELETE RESTRICT;

ALTER TABLE user_roles DROP CONSTRAINT user_roles_role_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_role_id_fkey FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE RESTRICT;

ALTER TABLE user_roles DROP CONSTRAINT user_roles_user_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE RESTRICT;

-- +goose Down
ALTER TABLE accounts DROP CONSTRAINT accounts_parent_id_fkey;
ALTER TABLE accounts ADD CONSTRAINT accounts_parent_id_fkey FOREIGN KEY (parent_id) REFERENCES accounts(id);

ALTER TABLE activities DROP CONSTRAINT activities_leader_id_fkey;
ALTER TABLE activities ADD CONSTRAINT activities_leader_id_fkey FOREIGN KEY (leader_id) REFERENCES users(id);

ALTER TABLE activity_participants DROP CONSTRAINT activity_participants_activity_id_fkey;
ALTER TABLE activity_participants ADD CONSTRAINT activity_participants_activity_id_fkey FOREIGN KEY (activity_id) REFERENCES activities(id) ON DELETE CASCADE;

ALTER TABLE activity_participants DROP CONSTRAINT activity_participants_added_by_fkey;
ALTER TABLE activity_participants ADD CONSTRAINT activity_participants_added_by_fkey FOREIGN KEY (added_by) REFERENCES users(id);

ALTER TABLE activity_participants DROP CONSTRAINT activity_participants_user_id_fkey;
ALTER TABLE activity_participants ADD CONSTRAINT activity_participants_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE activity_resources DROP CONSTRAINT activity_resources_activity_id_fkey;
ALTER TABLE activity_resources ADD CONSTRAINT activity_resources_activity_id_fkey FOREIGN KEY (activity_id) REFERENCES activities(id) ON DELETE CASCADE;

ALTER TABLE activity_resources DROP CONSTRAINT activity_resources_author_id_fkey;
ALTER TABLE activity_resources ADD CONSTRAINT activity_resources_author_id_fkey FOREIGN KEY (author_id) REFERENCES users(id);

ALTER TABLE board_members DROP CONSTRAINT board_members_user_id_fkey;
ALTER TABLE board_members ADD CONSTRAINT board_members_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE budget_item_accounts DROP CONSTRAINT budget_item_accounts_account_id_fkey;
ALTER TABLE budget_item_accounts ADD CONSTRAINT budget_item_accounts_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);

ALTER TABLE budget_item_accounts DROP CONSTRAINT budget_item_accounts_item_id_budget_id_fkey;
ALTER TABLE budget_item_accounts ADD CONSTRAINT budget_item_accounts_item_id_budget_id_fkey FOREIGN KEY (item_id, budget_id) REFERENCES budget_items(id, budget_id) ON DELETE CASCADE;

ALTER TABLE budget_items DROP CONSTRAINT budget_items_budget_id_fkey;
ALTER TABLE budget_items ADD CONSTRAINT budget_items_budget_id_fkey FOREIGN KEY (budget_id) REFERENCES budgets(id) ON DELETE CASCADE;

ALTER TABLE distribution_articles DROP CONSTRAINT distribution_articles_distribution_id_fkey;
ALTER TABLE distribution_articles ADD CONSTRAINT distribution_articles_distribution_id_fkey FOREIGN KEY (distribution_id) REFERENCES distributions(id) ON DELETE CASCADE;

ALTER TABLE distribution_options DROP CONSTRAINT distribution_options_distribution_id_fkey;
ALTER TABLE distribution_options ADD CONSTRAINT distribution_options_distribution_id_fkey FOREIGN KEY (distribution_id) REFERENCES distributions(id) ON DELETE CASCADE;

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_beneficiary_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_beneficiary_id_fkey FOREIGN KEY (beneficiary_id) REFERENCES users(id);

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_distribution_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_distribution_id_fkey FOREIGN KEY (distribution_id) REFERENCES distributions(id) ON DELETE CASCADE;

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_recorded_by_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_recorded_by_id_fkey FOREIGN KEY (recorded_by_id) REFERENCES users(id);

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_transaction_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id);

ALTER TABLE distribution_payments DROP CONSTRAINT distribution_payments_unit_id_fkey;
ALTER TABLE distribution_payments ADD CONSTRAINT distribution_payments_unit_id_fkey FOREIGN KEY (unit_id) REFERENCES distribution_units(id) ON DELETE SET NULL;

ALTER TABLE distribution_unit_identifiers DROP CONSTRAINT distribution_unit_identifiers_article_id_fkey;
ALTER TABLE distribution_unit_identifiers ADD CONSTRAINT distribution_unit_identifiers_article_id_fkey FOREIGN KEY (article_id) REFERENCES distribution_articles(id) ON DELETE CASCADE;

ALTER TABLE distribution_unit_identifiers DROP CONSTRAINT distribution_unit_identifiers_paid_marked_by_id_fkey;
ALTER TABLE distribution_unit_identifiers ADD CONSTRAINT distribution_unit_identifiers_paid_marked_by_id_fkey FOREIGN KEY (paid_marked_by_id) REFERENCES users(id);

ALTER TABLE distribution_unit_identifiers DROP CONSTRAINT distribution_unit_identifiers_unit_id_fkey;
ALTER TABLE distribution_unit_identifiers ADD CONSTRAINT distribution_unit_identifiers_unit_id_fkey FOREIGN KEY (unit_id) REFERENCES distribution_units(id) ON DELETE CASCADE;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_article_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_article_id_fkey FOREIGN KEY (article_id) REFERENCES distribution_articles(id) ON DELETE CASCADE;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_beneficiary_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_beneficiary_id_fkey FOREIGN KEY (beneficiary_id) REFERENCES users(id);

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_distribution_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_distribution_id_fkey FOREIGN KEY (distribution_id) REFERENCES distributions(id) ON DELETE CASCADE;

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_paid_marked_by_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_paid_marked_by_id_fkey FOREIGN KEY (paid_marked_by_id) REFERENCES users(id);

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_picked_up_by_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_picked_up_by_id_fkey FOREIGN KEY (picked_up_by_id) REFERENCES users(id);

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_pickup_recorded_by_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_pickup_recorded_by_id_fkey FOREIGN KEY (pickup_recorded_by_id) REFERENCES users(id);

ALTER TABLE distribution_units DROP CONSTRAINT distribution_units_registered_by_id_fkey;
ALTER TABLE distribution_units ADD CONSTRAINT distribution_units_registered_by_id_fkey FOREIGN KEY (registered_by_id) REFERENCES users(id);

ALTER TABLE distributions DROP CONSTRAINT distributions_account_id_fkey;
ALTER TABLE distributions ADD CONSTRAINT distributions_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);

ALTER TABLE distributions DROP CONSTRAINT distributions_created_by_fkey;
ALTER TABLE distributions ADD CONSTRAINT distributions_created_by_fkey FOREIGN KEY (created_by) REFERENCES users(id);

ALTER TABLE licenses DROP CONSTRAINT licenses_license_type_id_fkey;
ALTER TABLE licenses ADD CONSTRAINT licenses_license_type_id_fkey FOREIGN KEY (license_type_id) REFERENCES license_types(id);

ALTER TABLE licenses DROP CONSTRAINT licenses_transaction_id_fkey;
ALTER TABLE licenses ADD CONSTRAINT licenses_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id);

ALTER TABLE licenses DROP CONSTRAINT licenses_user_id_fkey;
ALTER TABLE licenses ADD CONSTRAINT licenses_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE magic_link_tokens DROP CONSTRAINT magic_link_tokens_user_id_fkey;
ALTER TABLE magic_link_tokens ADD CONSTRAINT magic_link_tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE material_items DROP CONSTRAINT material_items_type_fkey;
ALTER TABLE material_items ADD CONSTRAINT material_items_type_fkey FOREIGN KEY (type_id, kind) REFERENCES material_types(id, kind);

ALTER TABLE material_movements DROP CONSTRAINT material_movements_activity_id_fkey;
ALTER TABLE material_movements ADD CONSTRAINT material_movements_activity_id_fkey FOREIGN KEY (activity_id) REFERENCES activities(id);

ALTER TABLE material_movements DROP CONSTRAINT material_movements_item_id_fkey;
ALTER TABLE material_movements ADD CONSTRAINT material_movements_item_id_fkey FOREIGN KEY (item_id) REFERENCES material_items(id);

ALTER TABLE material_movements DROP CONSTRAINT material_movements_recorded_by_fkey;
ALTER TABLE material_movements ADD CONSTRAINT material_movements_recorded_by_fkey FOREIGN KEY (recorded_by) REFERENCES users(id);

ALTER TABLE material_movements DROP CONSTRAINT material_movements_user_id_fkey;
ALTER TABLE material_movements ADD CONSTRAINT material_movements_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

ALTER TABLE membership_dues DROP CONSTRAINT membership_dues_transaction_id_fkey;
ALTER TABLE membership_dues ADD CONSTRAINT membership_dues_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE membership_dues DROP CONSTRAINT membership_dues_user_id_fkey;
ALTER TABLE membership_dues ADD CONSTRAINT membership_dues_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id);

ALTER TABLE membership_leaves DROP CONSTRAINT membership_leaves_user_id_fkey;
ALTER TABLE membership_leaves ADD CONSTRAINT membership_leaves_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE memberships DROP CONSTRAINT memberships_user_id_fkey;
ALTER TABLE memberships ADD CONSTRAINT memberships_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE tokens DROP CONSTRAINT tokens_impersonated_by_fkey;
ALTER TABLE tokens ADD CONSTRAINT tokens_impersonated_by_fkey FOREIGN KEY (impersonated_by) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE tokens DROP CONSTRAINT tokens_user_id_fkey;
ALTER TABLE tokens ADD CONSTRAINT tokens_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE transaction_entries DROP CONSTRAINT transaction_entries_account_id_fkey;
ALTER TABLE transaction_entries ADD CONSTRAINT transaction_entries_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id);

ALTER TABLE transaction_entries DROP CONSTRAINT transaction_entries_transaction_id_fkey;
ALTER TABLE transaction_entries ADD CONSTRAINT transaction_entries_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id) ON DELETE CASCADE;

ALTER TABLE transaction_files DROP CONSTRAINT transaction_files_transaction_id_fkey;
ALTER TABLE transaction_files ADD CONSTRAINT transaction_files_transaction_id_fkey FOREIGN KEY (transaction_id) REFERENCES transactions(id);

ALTER TABLE transaction_files DROP CONSTRAINT transaction_files_uploaded_by_fkey;
ALTER TABLE transaction_files ADD CONSTRAINT transaction_files_uploaded_by_fkey FOREIGN KEY (uploaded_by) REFERENCES users(id) ON DELETE SET NULL;

ALTER TABLE user_roles DROP CONSTRAINT user_roles_role_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_role_id_fkey FOREIGN KEY (role_id) REFERENCES roles(id) ON DELETE CASCADE;

ALTER TABLE user_roles DROP CONSTRAINT user_roles_user_id_fkey;
ALTER TABLE user_roles ADD CONSTRAINT user_roles_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
