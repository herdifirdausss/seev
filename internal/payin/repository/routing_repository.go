package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/payin/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

type RoutingRepository interface {
	// ResolveCandidates returns EVERY enabled rule matching flow/userID/
	// currency/amount, ordered user-specific-first then by priority
	// (docs/plan/40 Task T2 — replaces the old single-winner Resolve so a
	// caller can skip a candidate whose circuit is open/unregistered and
	// fall through to the next). Empty slice = no rule matched at all.
	ResolveCandidates(ctx context.Context, flow string, userID uuid.UUID, currency string, amount int64) ([]model.RoutingCandidate, error)
	ListRules(ctx context.Context) ([]model.RoutingRule, error)
	CreateRule(ctx context.Context, rule model.RoutingRule) error
	UpdateRule(ctx context.Context, rule model.RoutingRule) error
	GetVendorGateway(ctx context.Context, vendor string) (model.VendorGateway, bool, error)
	ListVendorGateways(ctx context.Context) ([]model.VendorGateway, error)
	UpsertVendorGateway(ctx context.Context, mapping model.VendorGateway) error
}

type routingRepo struct{ db database.DatabaseSQL }

func NewRoutingRepository(db database.DatabaseSQL) RoutingRepository { return &routingRepo{db: db} }

func (r *routingRepo) ResolveCandidates(ctx context.Context, flow string, userID uuid.UUID, currency string, amount int64) ([]model.RoutingCandidate, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT rr.vendor, vg.gateway
		FROM payin_routing_rules rr
		JOIN payin_vendor_gateways vg ON vg.vendor = rr.vendor
		WHERE rr.enabled AND rr.flow = $1
		  AND (rr.user_id = $2 OR rr.user_id IS NULL)
		  AND (rr.currency = $3 OR rr.currency IS NULL)
		  AND (rr.min_amount IS NULL OR $4 >= rr.min_amount)
		  AND (rr.max_amount IS NULL OR $4 <= rr.max_amount)
		ORDER BY (rr.user_id IS NOT NULL) DESC, rr.priority ASC`, flow, userID, currency, amount)
	if err != nil {
		return nil, fmt.Errorf("resolve payin route candidates: %w", err)
	}
	defer rows.Close()
	var out []model.RoutingCandidate
	for rows.Next() {
		var c model.RoutingCandidate
		if err := rows.Scan(&c.Vendor, &c.Gateway); err != nil {
			return nil, fmt.Errorf("scan payin route candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func setNullableRuleFields(rule *model.RoutingRule, currency sql.NullString, minAmount, maxAmount sql.NullInt64, userID uuid.NullUUID) {
	if currency.Valid {
		rule.Currency = &currency.String
	}
	if minAmount.Valid {
		rule.MinAmount = &minAmount.Int64
	}
	if maxAmount.Valid {
		rule.MaxAmount = &maxAmount.Int64
	}
	if userID.Valid {
		rule.UserID = &userID.UUID
	}
}

func (r *routingRepo) ListRules(ctx context.Context) ([]model.RoutingRule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, flow, priority, enabled, currency, min_amount, max_amount, user_id, vendor, created_at, updated_at FROM payin_routing_rules ORDER BY priority`)
	if err != nil {
		return nil, fmt.Errorf("list payin routing rules: %w", err)
	}
	defer rows.Close()
	var out []model.RoutingRule
	for rows.Next() {
		var rule model.RoutingRule
		var currency sql.NullString
		var minAmount, maxAmount sql.NullInt64
		var userID uuid.NullUUID
		if err := rows.Scan(&rule.ID, &rule.Flow, &rule.Priority, &rule.Enabled, &currency, &minAmount, &maxAmount, &userID, &rule.Vendor, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan payin routing rule: %w", err)
		}
		setNullableRuleFields(&rule, currency, minAmount, maxAmount, userID)
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (r *routingRepo) CreateRule(ctx context.Context, rule model.RoutingRule) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO payin_routing_rules (id, flow, priority, enabled, currency, min_amount, max_amount, user_id, vendor) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, rule.ID, rule.Flow, rule.Priority, rule.Enabled, rule.Currency, rule.MinAmount, rule.MaxAmount, rule.UserID, rule.Vendor)
	if err != nil {
		return fmt.Errorf("create payin routing rule: %w", err)
	}
	return nil
}

func (r *routingRepo) UpdateRule(ctx context.Context, rule model.RoutingRule) error {
	result, err := r.db.ExecContext(ctx, `UPDATE payin_routing_rules SET flow=$2, priority=$3, enabled=$4, currency=$5, min_amount=$6, max_amount=$7, user_id=$8, vendor=$9, updated_at=now() WHERE id=$1`, rule.ID, rule.Flow, rule.Priority, rule.Enabled, rule.Currency, rule.MinAmount, rule.MaxAmount, rule.UserID, rule.Vendor)
	if err != nil {
		return fmt.Errorf("update payin routing rule: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *routingRepo) GetVendorGateway(ctx context.Context, vendor string) (model.VendorGateway, bool, error) {
	var out model.VendorGateway
	err := r.db.QueryRowContext(ctx, `SELECT vendor, gateway FROM payin_vendor_gateways WHERE vendor=$1`, vendor).Scan(&out.Vendor, &out.Gateway)
	if errors.Is(err, sql.ErrNoRows) {
		return model.VendorGateway{}, false, nil
	}
	if err != nil {
		return model.VendorGateway{}, false, fmt.Errorf("get payin vendor gateway: %w", err)
	}
	return out, true, nil
}

func (r *routingRepo) ListVendorGateways(ctx context.Context) ([]model.VendorGateway, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT vendor, gateway FROM payin_vendor_gateways ORDER BY vendor`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.VendorGateway
	for rows.Next() {
		var item model.VendorGateway
		if err := rows.Scan(&item.Vendor, &item.Gateway); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *routingRepo) UpsertVendorGateway(ctx context.Context, mapping model.VendorGateway) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO payin_vendor_gateways (vendor, gateway) VALUES ($1,$2) ON CONFLICT (vendor) DO UPDATE SET gateway=EXCLUDED.gateway`, mapping.Vendor, mapping.Gateway)
	if err != nil {
		return fmt.Errorf("upsert payin vendor gateway: %w", err)
	}
	return nil
}
