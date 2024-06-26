package postgres

import (
	"context"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/mskKote/prospero_backend/pkg/config"
	"github.com/mskKote/prospero_backend/pkg/lib"
	"github.com/mskKote/prospero_backend/pkg/logging"
	"go.uber.org/zap"
	"time"
)

type Client interface {
	Exec(ctx context.Context, sql string, arguments ...interface{}) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
}

var (
	logger = logging.GetLogger()
	cfg    = config.GetConfig()
)

func NewClient(ctx context.Context, maxAttempts int) (pool *pgxpool.Pool, err error) {

	dsn := fmt.Sprintf("postgresql://%s:%s@%s:%s/%s",
		cfg.Postgres.Username,
		cfg.Postgres.Password,
		cfg.Postgres.Host,
		cfg.Postgres.Port,
		cfg.Postgres.Database)
	//logger.InfoContext(ctx, dsn)

	err = lib.DoWithTries(func() error {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		if pool, err = pgxpool.New(ctx, dsn); err != nil {
			logger.Error("Проблемы с подключением", zap.Error(err))
			return err
		}

		return nil
	}, maxAttempts, 5*time.Second)

	if err != nil {
		logger.Fatal("error do with tries postgresql", zap.Error(err))
	}

	if err1 := pool.Ping(ctx); err1 != nil {
		logger.Fatal("Не подключились!", zap.Error(err1))
	}

	return pool, nil
}
