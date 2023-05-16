package main

import (
	"context"
	"github.com/gin-gonic/gin"
	internalMetrics "github.com/mskKote/prospero_backend/internal/adapters/metrics"
	"github.com/mskKote/prospero_backend/internal/controller/http/v1/routes"
	"github.com/mskKote/prospero_backend/internal/domain/usecase/RSS"
	"github.com/mskKote/prospero_backend/internal/domain/usecase/search"
	"github.com/mskKote/prospero_backend/internal/domain/usecase/sources"
	"github.com/mskKote/prospero_backend/pkg/config"
	"github.com/mskKote/prospero_backend/pkg/logging"
	pkgMetrics "github.com/mskKote/prospero_backend/pkg/metrics"
	"github.com/mskKote/prospero_backend/pkg/tracing"
	"github.com/uptrace/opentelemetry-go-extra/otelzap"
	"go.uber.org/zap"
	"time"
)

var (
	cfg    = config.GetConfig()
	logger = logging.GetLogger()
)

func main() {
	startup(cfg)
}

func startup(cfg *config.Config) {

	// --------------------------------------- GIN SETUP
	r := gin.New() // empty engine
	if cfg.IsDebug == false {
		gin.SetMode(gin.ReleaseMode)
	}
	// TODO: достать массив из cfg
	//err := r.SetTrustedProxies([]string{"127.0.0.1"})
	//if err != nil {
	//	logger.Fatal("Не получилось установить proxy", zap.Error(err))
	//}

	// --------------------------------------- MIDDLEWARE
	// Recovery
	r.Use(gin.Recovery())

	// Logging
	if cfg.Logger.UseDefaultGin {
		logger.Info("Используем DefaultGin")
		r.Use(gin.Logger())
	}
	//if cfg.Logger.ToGraylog { // logrus & graylog
	//	logger.Info("Используем Graylog")
	//	r.Use(logging.GraylogMiddlewareLogger())
	//}
	if cfg.Logger.UseZap {
		logger.Info("Используем Zap")
		logging.ZapMiddlewareLogger(r)
		undo := otelzap.ReplaceGlobals(logger.Logger)
		defer undo()
		defer func(loggerZap *zap.Logger) {
			err := loggerZap.Sync()
			if err != nil {
				loggerZap.Error("Не получилось синхронизироваться", zap.Error(err))
			}
		}(logger.Logger.Logger)
	}

	// Tracing
	if cfg.Tracing {
		tp := tracing.Startup(r)
		ctx, cancel := context.WithCancel(context.Background())

		// Cleanly shutdown and flush telemetry when the application exits.
		defer func(ctx context.Context) {
			// Do not make the application hang when it is shutdown.
			ctx, cancel = context.WithTimeout(ctx, time.Second*5)
			defer cancel()
			if err := tp.Shutdown(ctx); err != nil {
				logger.Fatal("Ошибка при выключении", zap.Error(err))
			}
		}(ctx)
	}

	// Metrics
	if cfg.Metrics {
		p := pkgMetrics.Startup(r)
		internalMetrics.RegisterMetrics(p)
	}

	// --------------------------------------- ROUTES
	apiV1 := r.Group("/api/v1")
	{
		routes.
			NewSearchRoutes(&search.Usecase{}).
			RegisterSearch(apiV1)
		routes.
			NewSourcesRoutes(&sources.Usecase{}).
			RegisterSources(apiV1)
	}

	// TODO: аутентификация, список админов
	//admin := r.Group("/admin/api/v1")
	//{
	//
	//}

	// --------------------------------------- IGNITION
	go (&RSS.Usecase{}).Startup()

	if err := r.Run(":" + cfg.Port); err != nil {
		logger.Fatal("ошибка, завершаем программу", zap.Error(err))
	}
}
