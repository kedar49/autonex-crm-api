package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	dbURL := getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/gocrm?sslmode=disable")
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		log.Fatalf("Unable to connect to database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		log.Fatalf("Ping failed: %v", err)
	}

	fmt.Println("Connected to database. Seeding baseline CRM records...")

	tx, err := pool.Begin(ctx)
	if err != nil {
		log.Fatalf("Begin transaction failed: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// 1. Organization
	var orgID string
	err = tx.QueryRow(ctx, `
		INSERT INTO organizations (name, currency)
		VALUES ('Acme Corp', 'USD')
		ON CONFLICT DO NOTHING
		RETURNING id::text
	`).Scan(&orgID)
	if err != nil {
		// If exists, pick the first org
		err = tx.QueryRow(ctx, `SELECT id::text FROM organizations LIMIT 1`).Scan(&orgID)
		if err != nil {
			log.Fatalf("Failed to retrieve or create organization: %v", err)
		}
	}

	// 2. User & Profile
	var userID string
	err = tx.QueryRow(ctx, `
		INSERT INTO users (org_id, email, name, auth_provider)
		VALUES ($1::uuid, 'admin@acme.com', 'Acme Admin', 'email')
		ON CONFLICT (email) DO UPDATE SET name = EXCLUDED.name
		RETURNING id::text
	`, orgID).Scan(&userID)
	if err != nil {
		log.Fatalf("Failed to create/get user: %v", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO profiles (id, full_name, role)
		VALUES ($1::uuid, 'Acme Admin', 'owner')
		ON CONFLICT (id) DO UPDATE SET full_name = EXCLUDED.full_name
	`, userID)
	if err != nil {
		log.Fatalf("Failed to create/get profile: %v", err)
	}

	// 3. Company (Account)
	var companyID string
	err = tx.QueryRow(ctx, `
		INSERT INTO companies (org_id, name, domain, industry, city, website, owner_id)
		VALUES ($1::uuid, 'Stark Industries', 'stark.com', 'Technology', 'Los Angeles', 'https://stark.com', $2::uuid)
		RETURNING id::text
	`, orgID, userID).Scan(&companyID)
	if err != nil {
		log.Fatalf("Failed to create company: %v", err)
	}

	// 4. Contact
	var contactID string
	err = tx.QueryRow(ctx, `
		INSERT INTO contacts (org_id, company_id, first_name, last_name, email, phone, title)
		VALUES ($1::uuid, $2::uuid, 'Tony', 'Stark', 'tony@stark.com', '+1-555-0199', 'CEO')
		RETURNING id::text
	`, orgID, companyID).Scan(&contactID)
	if err != nil {
		log.Fatalf("Failed to create contact: %v", err)
	}

	// 5. Lead
	var leadID string
	err = tx.QueryRow(ctx, `
		INSERT INTO leads (
			org_id, company_id, contact_id, first_name, last_name, company,
			contact_name, email, phone, stage, status, assigned_to, value
		)
		VALUES (
			$1::uuid, $2::uuid, $3::uuid, 'Pepper', 'Potts', 'Stark Industries',
			'Pepper Potts', 'pepper@stark.com', '+1-555-0198', 'initial count', 'new', $4::uuid, 75000.00
		)
		RETURNING id::text
	`, orgID, companyID, contactID, userID).Scan(&leadID)
	if err != nil {
		log.Fatalf("Failed to create lead: %v", err)
	}

	// 6. Deal
	var dealID string
	err = tx.QueryRow(ctx, `
		INSERT INTO deals (
			org_id, company_id, contact_id, lead_id, title, stage, amount, owner_id, expected_close_date
		)
		VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::uuid, 'Arc Reactor Supply Deal', 'quote_sent', 250000.00, $5::uuid, $6
		)
		RETURNING id::text
	`, orgID, companyID, contactID, leadID, userID, time.Now().AddDate(0, 1, 0)).Scan(&dealID)
	if err != nil {
		log.Fatalf("Failed to create deal: %v", err)
	}

	// 7. Quote
	var quoteID string
	err = tx.QueryRow(ctx, `
		INSERT INTO quotes (
			org_id, deal_id, company_id, contact_id, owner_user_id, status, current_version, created_by, valid_until, currency, total
		)
		VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'draft', 1, $5::uuid, $6, 'USD', 250000.00
		)
		RETURNING id::text
	`, orgID, dealID, companyID, contactID, userID, time.Now().AddDate(0, 0, 30)).Scan(&quoteID)
	if err != nil {
		log.Fatalf("Failed to create quote: %v", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO quote_versions (quote_id, version_number, line_items, subtotal, tax, total, currency, is_current)
		VALUES ($1::uuid, 1, '[]'::jsonb, 250000.00, 0.00, 250000.00, 'USD', true)
	`, quoteID)
	if err != nil {
		log.Fatalf("Failed to create quote version: %v", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO quote_items (quote_id, org_id, position, description, quantity, unit_price, line_total)
		VALUES ($1::uuid, $2::uuid, 0, 'Clean Energy Module Units', 5, 50000.00, 250000.00)
	`, quoteID, orgID)
	if err != nil {
		log.Fatalf("Failed to create quote item: %v", err)
	}

	// 8. Invoice
	var invoiceID string
	invNumber := fmt.Sprintf("INV-%d", time.Now().Unix())
	err = tx.QueryRow(ctx, `
		INSERT INTO invoices (
			org_id, quote_id, company_id, contact_id, deal_id, owner_user_id,
			invoice_number, number, title, status, amount_due, currency, issue_date, due_date, total
		)
		VALUES (
			$1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, $6::uuid,
			$7, $7, 'Arc Reactor Initial Deposit', 'draft', 250000.00, 'USD', CURRENT_DATE, CURRENT_DATE + INTERVAL '30 days', 250000.00
		)
		RETURNING id::text
	`, orgID, quoteID, companyID, contactID, dealID, userID, invNumber).Scan(&invoiceID)
	if err != nil {
		log.Fatalf("Failed to create invoice: %v", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO invoice_items (invoice_id, org_id, position, description, quantity, unit_price, line_total)
		VALUES ($1::uuid, $2::uuid, 0, 'Arc Reactor Unit Initial Deposit', 5, 50000.00, 250000.00)
	`, invoiceID, orgID)
	if err != nil {
		log.Fatalf("Failed to create invoice item: %v", err)
	}

	// 9. Activity
	_, err = tx.Exec(ctx, `
		INSERT INTO activities (entity_type, entity_id, type, body, occurred_at, author_id)
		VALUES ('company', $1::uuid, 'note', 'Initial introduction call completed with Tony Stark.', now(), $2::uuid)
	`, companyID, userID)
	if err != nil {
		log.Fatalf("Failed to create activity: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		log.Fatalf("Failed to commit seed transaction: %v", err)
	}

	fmt.Println("Seed completed successfully!")
	fmt.Printf("Created Records:\n  Org ID: %s\n  User ID: %s\n  Company ID: %s\n  Contact ID: %s\n  Lead ID: %s\n  Deal ID: %s\n  Quote ID: %s\n  Invoice ID: %s\n",
		orgID, userID, companyID, contactID, leadID, dealID, quoteID, invoiceID)
}
