package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

var scheduledTestCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ScheduledTestService provides CRUD operations for scheduled test plans and results.
type ScheduledTestService struct {
	planRepo   ScheduledTestPlanRepository
	resultRepo ScheduledTestResultRepository
}

// NewScheduledTestService creates a new ScheduledTestService.
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
) *ScheduledTestService {
	return &ScheduledTestService{
		planRepo:   planRepo,
		resultRepo: resultRepo,
	}
}

// CreatePlan validates the cron expression, computes next_run_at, and persists the plan.
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Create(ctx, plan)
}

// GetPlan retrieves a plan by ID.
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount returns all plans for a given account.
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// BatchUpsertScheduledTestPlanRequest contains shared fields for batch plan upsert.
type BatchUpsertScheduledTestPlanRequest struct {
	AccountIDs     []int64 `json:"account_ids"`
	ModelID        string  `json:"model_id"`
	CronExpression string  `json:"cron_expression"`
	Enabled        *bool   `json:"enabled"`
	MaxResults     int     `json:"max_results"`
	AutoRecover    *bool   `json:"auto_recover"`
}

// BatchUpsertScheduledTestPlanItem reports the result for one account.
type BatchUpsertScheduledTestPlanItem struct {
	AccountID int64              `json:"account_id"`
	Action    string             `json:"action"`
	Plan      *ScheduledTestPlan `json:"plan,omitempty"`
	Error     string             `json:"error,omitempty"`
}

// BatchUpsertScheduledTestPlanResult reports aggregate batch upsert results.
type BatchUpsertScheduledTestPlanResult struct {
	Created int                                `json:"created"`
	Updated int                                `json:"updated"`
	Failed  int                                `json:"failed"`
	Items   []BatchUpsertScheduledTestPlanItem `json:"items"`
}

// BatchUpsertPlans creates or updates plans by account_id and model_id, tolerating per-account failures.
func (s *ScheduledTestService) BatchUpsertPlans(ctx context.Context, req BatchUpsertScheduledTestPlanRequest) BatchUpsertScheduledTestPlanResult {
	result := BatchUpsertScheduledTestPlanResult{Items: make([]BatchUpsertScheduledTestPlanItem, 0, len(req.AccountIDs))}
	for _, accountID := range req.AccountIDs {
		item := s.batchUpsertPlanForAccount(ctx, req, accountID)
		switch item.Action {
		case "created":
			result.Created++
		case "updated":
			result.Updated++
		default:
			result.Failed++
		}
		result.Items = append(result.Items, item)
	}
	return result
}

func (s *ScheduledTestService) batchUpsertPlanForAccount(ctx context.Context, req BatchUpsertScheduledTestPlanRequest, accountID int64) BatchUpsertScheduledTestPlanItem {
	item := BatchUpsertScheduledTestPlanItem{AccountID: accountID}
	if accountID <= 0 {
		item.Action = "failed"
		item.Error = "invalid account id"
		return item
	}

	existing, err := s.planRepo.GetByAccountIDAndModelID(ctx, accountID, req.ModelID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		item.Action = "failed"
		item.Error = err.Error()
		return item
	}

	if errors.Is(err, sql.ErrNoRows) {
		return s.createBatchPlan(ctx, req, accountID)
	}
	return s.updateBatchPlan(ctx, req, existing)
}

func (s *ScheduledTestService) createBatchPlan(ctx context.Context, req BatchUpsertScheduledTestPlanRequest, accountID int64) BatchUpsertScheduledTestPlanItem {
	plan := &ScheduledTestPlan{
		AccountID:      accountID,
		ModelID:        req.ModelID,
		CronExpression: req.CronExpression,
		Enabled:        true,
		MaxResults:     req.MaxResults,
	}
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if req.AutoRecover != nil {
		plan.AutoRecover = *req.AutoRecover
	}

	created, err := s.CreatePlan(ctx, plan)
	if err != nil {
		return BatchUpsertScheduledTestPlanItem{AccountID: accountID, Action: "failed", Error: err.Error()}
	}
	return BatchUpsertScheduledTestPlanItem{AccountID: accountID, Action: "created", Plan: created}
}

func (s *ScheduledTestService) updateBatchPlan(ctx context.Context, req BatchUpsertScheduledTestPlanRequest, plan *ScheduledTestPlan) BatchUpsertScheduledTestPlanItem {
	plan.ModelID = req.ModelID
	plan.CronExpression = req.CronExpression
	if req.Enabled != nil {
		plan.Enabled = *req.Enabled
	}
	if req.MaxResults > 0 {
		plan.MaxResults = req.MaxResults
	}
	if req.AutoRecover != nil {
		plan.AutoRecover = *req.AutoRecover
	}

	updated, err := s.UpdatePlan(ctx, plan)
	if err != nil {
		return BatchUpsertScheduledTestPlanItem{AccountID: plan.AccountID, Action: "failed", Error: err.Error()}
	}
	return BatchUpsertScheduledTestPlanItem{AccountID: plan.AccountID, Action: "updated", Plan: updated}
}

// UpdatePlan validates cron and updates the plan.
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	nextRun, err := computeNextRun(plan.CronExpression, time.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

// DeletePlan removes a plan and its results (via CASCADE).
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults returns the most recent results for a plan.
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}

// SaveResult inserts a result and prunes old entries beyond maxResults.
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	result.PlanID = planID
	if _, err := s.resultRepo.Create(ctx, result); err != nil {
		return err
	}
	return s.resultRepo.PruneOldResults(ctx, planID, maxResults)
}

func computeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := scheduledTestCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, err
	}
	return sched.Next(from), nil
}
