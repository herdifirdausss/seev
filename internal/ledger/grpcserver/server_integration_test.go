//go:build integration

package grpcserver_test

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/structpb"

	ledgerv1 "github.com/herdifirdausss/seev/gen/ledger/v1"
	"github.com/herdifirdausss/seev/internal/ledger"
	"github.com/herdifirdausss/seev/internal/testutil"
	"github.com/herdifirdausss/seev/pkg/database"
	"github.com/herdifirdausss/seev/pkg/messaging"
)

func TestPostMoneyInEndToEndOverGRPC(t *testing.T) {
	ctx := context.Background()
	const dbName, dbUser, dbPassword = "seev_ledger_test", "test", "secret"
	container, err := postgres.Run(ctx, "postgres:16.14-alpine",
		postgres.WithDatabase(dbName), postgres.WithUsername(dbUser), postgres.WithPassword(dbPassword),
		postgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "5432")
	require.NoError(t, err)
	dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable", dbUser, dbPassword, host, port.Port(), dbName)
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	migrations := "file://" + filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "migrations")
	require.NoError(t, testutil.ApplyServiceMigrations(migrations, dsn))

	db, err := database.New(ctx, database.Config{
		Host: host, Port: port.Port(), User: dbUser, Password: dbPassword,
		DB: dbName, SSLMode: "disable", MaxOpenConns: 10,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	module := ledger.NewModule(db, &messaging.MockBroker{}, nil, ledger.WorkerConfig{}, slog.Default(), decimal.Zero, nil, nil, 0)
	require.NoError(t, module.LoadCurrencies(ctx))
	userID := uuid.New()
	_, err = module.ProvisionUser(ctx, userID, "IDR")
	require.NoError(t, err)

	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	module.RegisterGRPC(grpcServer)
	go func() { _ = grpcServer.Serve(listener) }()
	t.Cleanup(grpcServer.Stop)
	connectCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	conn, err := grpc.DialContext(connectCtx, "bufnet", //nolint:staticcheck // bufconn requires the legacy blocking dial API.
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock(), //nolint:staticcheck
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	client := ledgerv1.NewLedgerServiceClient(conn)
	metadata, err := structpb.NewStruct(map[string]any{"gateway": "bca", "external_ref": "grpc-e2e"})
	require.NoError(t, err)

	_, err = client.Post(ctx, &ledgerv1.PostRequest{
		IdempotencyKey: "grpc-money-in", IdempotencyScope: userID.String(), Type: "money_in",
		Amount: "100000", UserId: userID.String(), Metadata: metadata,
	})
	require.NoError(t, err)
	tx, err := client.GetTransactionByIdempotencyKey(ctx, &ledgerv1.GetTxByIdemKeyRequest{
		IdempotencyKey: "grpc-money-in", IdempotencyScope: userID.String(),
	})
	require.NoError(t, err)
	require.Equal(t, "posted", tx.Status)
	require.Equal(t, "100000", tx.Amount)

	// docs/roadmap/archive/33 T2: one global rule and one per-user override must price
	// two otherwise-identical transfers differently and land as distinct
	// fee entries in the real ledger.
	userB := uuid.New()
	_, err = module.ProvisionUser(ctx, userB, "IDR")
	require.NoError(t, err)
	_, err = client.Post(ctx, &ledgerv1.PostRequest{
		IdempotencyKey: "grpc-money-in-b", IdempotencyScope: userB.String(), Type: "money_in",
		Amount: "100000", UserId: userB.String(), Metadata: metadata,
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO fee_rules
		(id,tx_type,gateway,currency,user_id,flat_minor_units) VALUES
		($1,'transfer_p2p','','IDR',NULL,300),
		($2,'transfer_p2p','','IDR',$3,700)`, uuid.New(), uuid.New(), userID)
	require.NoError(t, err)

	postPricedTransfer := func(key string, from, to uuid.UUID, wantFee string) {
		feeResponse, resolveErr := client.ResolveFee(ctx, &ledgerv1.ResolveFeeRequest{
			Type: "transfer_p2p", Currency: "IDR", Amount: "10000", UserId: from.String(),
		})
		require.NoError(t, resolveErr)
		require.True(t, feeResponse.Ok)
		require.Equal(t, wantFee, feeResponse.Fee)
		feeMetadata, metadataErr := structpb.NewStruct(map[string]any{
			"fee_amount": feeResponse.Fee, "fee_gateway": feeResponse.FeeGateway,
		})
		require.NoError(t, metadataErr)
		_, postErr := client.Post(ctx, &ledgerv1.PostRequest{
			IdempotencyKey: key, IdempotencyScope: from.String(), Type: "transfer_p2p",
			Amount: "10000", UserId: from.String(), TargetUserId: to.String(), Metadata: feeMetadata,
		})
		require.NoError(t, postErr)
		var entryFee int64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT e.amount
			FROM ledger_entries e
			JOIN ledger_transactions tx ON tx.id=e.transaction_id
			JOIN accounts a ON a.id=e.account_id
			WHERE tx.idempotency_key=$1 AND a.type='fee' AND a.system_qualifier='platform'`, key).Scan(&entryFee))
		require.Equal(t, wantFee, decimal.NewFromInt(entryFee).String())
	}
	postPricedTransfer("grpc-transfer-user", userID, userB, "700")
	postPricedTransfer("grpc-transfer-global", userB, userID, "300")

	var unbalanced int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM fn_verify_ledger_balance('-infinity','infinity')").Scan(&unbalanced))
	require.Zero(t, unbalanced)
}
