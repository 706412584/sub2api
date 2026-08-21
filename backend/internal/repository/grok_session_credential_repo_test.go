package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestGrokSessionCredentialRepo_Save(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewGrokSessionCredentialRepository(db)

	session := &service.GrokSessionCredential{
		AccountID:          123,
		Source:             "console",
		EncryptedSSO:       "encrypted_sso_data",
		EncryptedBrowserUA: "encrypted_ua",
		Status:             "active",
	}

	mock.ExpectExec(`INSERT INTO grok_session_credentials`).
		WithArgs(
			session.AccountID,
			session.Source,
			session.EncryptedSSO,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			session.EncryptedBrowserUA,
			sqlmock.AnyArg(),
			session.Status,
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err = repo.Save(context.Background(), session)
	if err != nil {
		t.Errorf("Save failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGrokSessionCredentialRepo_GetByAccountID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewGrokSessionCredentialRepository(db)

	now := time.Now()
	rows := sqlmock.NewRows([]string{
		"account_id", "source", "encrypted_sso", "encrypted_sso_rw", "encrypted_cf_clearance",
		"encrypted_browser_ua", "bound_proxy_id", "status", "last_error_code", "last_error_at",
		"web_tier", "web_terms_version", "web_terms_accepted_at", "web_birth_date_set_at",
		"web_nsfw_enabled_at", "created_at", "updated_at",
	}).AddRow(
		123, "console", "encrypted_sso", nil, nil,
		"encrypted_ua", nil, "active", nil, nil,
		nil, nil, nil, nil,
		nil, now, now,
	)

	mock.ExpectQuery(`SELECT .+ FROM grok_session_credentials WHERE account_id`).
		WithArgs(123).
		WillReturnRows(rows)

	session, err := repo.GetByAccountID(context.Background(), 123)
	if err != nil {
		t.Errorf("GetByAccountID failed: %v", err)
	}

	if session == nil {
		t.Fatal("expected session, got nil")
	}

	if session.AccountID != 123 {
		t.Errorf("expected AccountID 123, got %d", session.AccountID)
	}

	if session.Source != "console" {
		t.Errorf("expected Source console, got %s", session.Source)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}

func TestGrokSessionCredentialRepo_Delete(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer func() { _ = db.Close() }()

	repo := NewGrokSessionCredentialRepository(db)

	mock.ExpectExec(`DELETE FROM grok_session_credentials WHERE account_id`).
		WithArgs(123).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = repo.Delete(context.Background(), 123)
	if err != nil {
		t.Errorf("Delete failed: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unfulfilled expectations: %v", err)
	}
}
