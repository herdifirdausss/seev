package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/herdifirdausss/seev/internal/payout/model"
	"github.com/herdifirdausss/seev/pkg/database"
)

type RoutingRepository interface {
	Resolve(context.Context, string, uuid.UUID, string, int64) (model.RoutingRule, string, bool, error)
	ListRules(context.Context) ([]model.RoutingRule, error)
	CreateRule(context.Context, model.RoutingRule) error
	UpdateRule(context.Context, model.RoutingRule) error
	GetVendorGateway(context.Context, string) (model.VendorGateway, bool, error)
	UpsertVendorGateway(context.Context, model.VendorGateway) error
}

type routingRepo struct{ db database.DatabaseSQL }

func NewRoutingRepository(db database.DatabaseSQL) RoutingRepository { return &routingRepo{db: db} }

func (r *routingRepo) Resolve(ctx context.Context, flow string, userID uuid.UUID, currency string, amount int64) (model.RoutingRule, string, bool, error) {
	row := r.db.QueryRowContext(ctx, `SELECT rr.id, rr.flow, rr.priority, rr.enabled, rr.currency, rr.min_amount, rr.max_amount, rr.user_id, rr.vendor, rr.created_at, rr.updated_at, vg.gateway FROM payout_routing_rules rr JOIN payout_vendor_gateways vg ON vg.vendor=rr.vendor WHERE rr.enabled AND rr.flow=$1 AND (rr.user_id=$2 OR rr.user_id IS NULL) AND (rr.currency=$3 OR rr.currency IS NULL) AND (rr.min_amount IS NULL OR $4>=rr.min_amount) AND (rr.max_amount IS NULL OR $4<=rr.max_amount) ORDER BY (rr.user_id IS NOT NULL) DESC, rr.priority ASC LIMIT 1`, flow, userID, currency, amount)
	rule, gateway, err := scanResolved(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.RoutingRule{}, "", false, nil
	}
	if err != nil {
		return model.RoutingRule{}, "", false, fmt.Errorf("resolve payout route: %w", err)
	}
	return rule, gateway, true, nil
}

func scanResolved(row *sql.Row) (model.RoutingRule, string, error) {
	var rule model.RoutingRule
	var currency sql.NullString
	var min, max sql.NullInt64
	var user uuid.NullUUID
	var gateway string
	err := row.Scan(&rule.ID, &rule.Flow, &rule.Priority, &rule.Enabled, &currency, &min, &max, &user, &rule.Vendor, &rule.CreatedAt, &rule.UpdatedAt, &gateway)
	setNullable(&rule, currency, min, max, user)
	return rule, gateway, err
}
func setNullable(rule *model.RoutingRule, currency sql.NullString, min, max sql.NullInt64, user uuid.NullUUID) {
	if currency.Valid {
		rule.Currency = &currency.String
	}
	if min.Valid {
		rule.MinAmount = &min.Int64
	}
	if max.Valid {
		rule.MaxAmount = &max.Int64
	}
	if user.Valid {
		rule.UserID = &user.UUID
	}
}

func (r *routingRepo) ListRules(ctx context.Context) ([]model.RoutingRule, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, flow, priority, enabled, currency, min_amount, max_amount, user_id, vendor, created_at, updated_at FROM payout_routing_rules ORDER BY priority`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.RoutingRule
	for rows.Next() {
		var rule model.RoutingRule
		var currency sql.NullString
		var min, max sql.NullInt64
		var user uuid.NullUUID
		if err := rows.Scan(&rule.ID, &rule.Flow, &rule.Priority, &rule.Enabled, &currency, &min, &max, &user, &rule.Vendor, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		setNullable(&rule, currency, min, max, user)
		out = append(out, rule)
	}
	return out, rows.Err()
}
func (r *routingRepo) CreateRule(ctx context.Context, rule model.RoutingRule) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO payout_routing_rules (id,flow,priority,enabled,currency,min_amount,max_amount,user_id,vendor) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, rule.ID, rule.Flow, rule.Priority, rule.Enabled, rule.Currency, rule.MinAmount, rule.MaxAmount, rule.UserID, rule.Vendor)
	if err != nil {
		return fmt.Errorf("create payout routing rule: %w", err)
	}
	return nil
}
func (r *routingRepo) UpdateRule(ctx context.Context, rule model.RoutingRule) error {
	result, err := r.db.ExecContext(ctx, `UPDATE payout_routing_rules SET flow=$2,priority=$3,enabled=$4,currency=$5,min_amount=$6,max_amount=$7,user_id=$8,vendor=$9,updated_at=now() WHERE id=$1`, rule.ID, rule.Flow, rule.Priority, rule.Enabled, rule.Currency, rule.MinAmount, rule.MaxAmount, rule.UserID, rule.Vendor)
	if err != nil {
		return err
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
	err := r.db.QueryRowContext(ctx, `SELECT vendor,gateway FROM payout_vendor_gateways WHERE vendor=$1`, vendor).Scan(&out.Vendor, &out.Gateway)
	if errors.Is(err, sql.ErrNoRows) {
		return model.VendorGateway{}, false, nil
	}
	if err != nil {
		return model.VendorGateway{}, false, err
	}
	return out, true, nil
}
func (r *routingRepo) UpsertVendorGateway(ctx context.Context, m model.VendorGateway) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO payout_vendor_gateways (vendor,gateway) VALUES ($1,$2) ON CONFLICT (vendor) DO UPDATE SET gateway=EXCLUDED.gateway`, m.Vendor, m.Gateway)
	return err
}
