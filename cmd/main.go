package main

import (
	"context"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	_ "github.com/mskKote/prospero_backend/docs"
	"github.com/mskKote/prospero_backend/internal/adapters/db/elastic/v8/articlesSearchRepository"
	"github.com/mskKote/prospero_backend/internal/adapters/db/elastic/v8/publisherSearchRepository"
	"github.com/mskKote/prospero_backend/internal/adapters/db/postgres/adminsRepository"
	"github.com/mskKote/prospero_backend/internal/adapters/db/postgres/publishersRepository"
	"github.com/mskKote/prospero_backend/internal/adapters/db/postgres/sourcesRepository"
	internalMetrics "github.com/mskKote/prospero_backend/internal/adapters/metrics"
	"github.com/mskKote/prospero_backend/internal/controller/http/v1/routes"
	"github.com/mskKote/prospero_backend/internal/domain/entity/admin"
	"github.com/mskKote/prospero_backend/internal/domain/service/adminService"
	"github.com/mskKote/prospero_backend/internal/domain/service/articleService"
	"github.com/mskKote/prospero_backend/internal/domain/service/publishersService"
	"github.com/mskKote/prospero_backend/internal/domain/service/sourcesService"
	"github.com/mskKote/prospero_backend/internal/domain/usecase/RSS"
	"github.com/mskKote/prospero_backend/internal/domain/usecase/adminka"
	"github.com/mskKote/prospero_backend/internal/domain/usecase/search"
	"github.com/mskKote/prospero_backend/internal/domain/usecase/service"
	"github.com/mskKote/prospero_backend/pkg/client/elastic"
	"github.com/mskKote/prospero_backend/pkg/client/postgres"
	"github.com/mskKote/prospero_backend/pkg/config"
	"github.com/mskKote/prospero_backend/pkg/logging"
	pkgMetrics "github.com/mskKote/prospero_backend/pkg/metrics"
	"github.com/mskKote/prospero_backend/pkg/security"
	"github.com/mskKote/prospero_backend/pkg/tracing"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"github.com/uptrace/opentelemetry-go-extra/otelzap"
	"go.uber.org/zap"
	"io"
	"log"
	"os"
	"time"
)

var (
	cfg    = config.GetConfig()
	logger = logging.GetLogger()
)

// @title			Prospero
// @version		1.0
// @description	News aggregator API
// @contact.name	Vitalii Popov
// @contact.url	https://www.linkedin.com/in/mskkote/
// @contact.email	msk.vitaly@gmail.com
// @host			localhost:80
// @BasePath		/
func main() {
	startup(cfg)
}

func startup(cfg *config.Config) {

	// --------------------------------------- DATABASES
	ctx := context.Background()
	pgClient, err := postgres.NewClient(ctx, 3)
	if err != nil {
		logger.Fatal("[POSTGRES] Не подключились к postgres", zap.Error(err))
	} else {
		logger.Info("[POSTGRES] УСПЕШНО подключилсь к POSTGRES!")
	}

	esClient, err := elastic.NewClient(ctx)
	if err != nil {
		logger.Fatal("[ELASTIC] Не подключились к elastic", zap.Error(err))
	} else {
		logger.Info("[ELASTIC] УСПЕШНО подключилсь к ELASTICSEARCH!")
	}

	sourcesREPO := sourcesRepository.New(pgClient)
	articlesREPO := articlesSearchRepository.New(esClient)
	publishersREPO := publishersRepository.New(pgClient)
	publishersSearchREPO := publishersSearchRepository.New(esClient)

	publishersSERVICE := publishersService.New(publishersREPO, publishersSearchREPO)
	articlesSERVICE := articleService.New(sourcesREPO, articlesREPO)
	sourcesSERVICE := sourcesService.New(sourcesREPO)

	if cfg.MigratePostgres {
		migrationsPg(pgClient, ctx)
	}
	if cfg.MigrateElastic {
		migrationsEs(articlesREPO, publishersSearchREPO, ctx)
	}

	// --------------------------------------- GIN
	r := gin.New()
	if cfg.IsDebug == false {
		gin.SetMode(gin.ReleaseMode)
	}

	corsCfg := cors.DefaultConfig()
	corsCfg.AllowAllOrigins = true
	corsCfg.AddExposeHeaders(tracing.ProsperoHeader)
	corsCfg.AddAllowHeaders("Authorization")
	r.Use(cors.New(corsCfg))

	// Recovery
	r.Use(gin.Recovery())

	// Logging
	if cfg.Logger.UseDefaultGin {
		logger.Info("Используем DefaultGin")
		r.Use(gin.Logger())
	}
	if cfg.Logger.UseZap {
		logger.Info("Используем Zap")
		logging.ZapMiddlewareLogger(r)
		undo := otelzap.ReplaceGlobals(logger.Logger)
		defer undo()
		defer func(loggerZap *zap.Logger) {
			err := loggerZap.Sync()
			if err != nil {
				loggerZap.Error("[LOGGER] Не получилось синхронизироваться", zap.Error(err))
			}
		}(logger.Logger.Logger)
	}

	// Tracing
	if cfg.UseTracingJaeger {
		tp := tracing.Startup(r)
		ctx, cancel := context.WithCancel(context.Background())

		// Cleanly shutdown and flush telemetry when the application exits.
		defer func(ctx context.Context) {
			// Do not make the application hang when it is shutdown.
			ctx, cancel = context.WithTimeout(ctx, time.Second*5)
			defer cancel()
			if err := tp.Shutdown(ctx); err != nil {
				logger.Fatal("[TRACING] Ошибка при выключении", zap.Error(err))
			}
		}(ctx)
	}

	// Metrics
	if cfg.Metrics {
		p := pkgMetrics.Startup(r)
		internalMetrics.RegisterMetrics(p)
	}

	// --------------------------------------- ROUTES
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	prosperoRoutes(r, &publishersSERVICE, &articlesSERVICE)
	adminkaStartup(r, pgClient, &sourcesSERVICE, &publishersSERVICE, &articlesSERVICE)
	serviceRoutes(r)

	logger.Info(fmt.Sprintf("adminkaStartup: %t", cfg.MigratePostgres))

	// --------------------------------------- IGNITION
	if cfg.UseCronSourcesRSS {
		go RSS.New(sourcesSERVICE, articlesSERVICE).Startup()
	}

	if err := r.Run(":" + cfg.Port); err != nil {
		logger.Fatal("ошибка, завершаем программу", zap.Error(err))
	}
}

func migrationsPg(client postgres.Client, ctx context.Context) {
	migration, err := os.OpenFile("./resources/migration_20230517_1.sql", os.O_RDONLY, 0666)
	if err != nil {
		logger.Fatal("[MIGRATION] Невозможно прочитать файл", zap.Error(err))
	}
	defer func(migration *os.File) {
		err := migration.Close()
		if err != nil {
			logger.Fatal("[MIGRATION] Невозможно закрыть файл миграции", zap.Error(err))
		}
	}(migration)

	data, err := io.ReadAll(migration)
	_, err = client.Exec(ctx, string(data))
	if err != nil {
		logger.Fatal("[MIGRATION] Миграции POSTGRES провалились", zap.Error(err))
	} else {
		logger.Info("[MIGRATION] УСПЕШНО мигрировали POSTGRES")
	}
}

func migrationsEs(
	a articlesSearchRepository.IRepository,
	p publishersSearchRepository.IRepository,
	ctx context.Context) {

	log.Printf("\n\n")
	p.Setup(ctx)
	a.Setup(ctx)
}

func adminkaStartup(
	r *gin.Engine,
	client postgres.Client,
	s *sourcesService.ISourceService,
	p *publishersService.IPublishersService,
	a *articleService.IArticleService) {

	adminREPO := adminsRepository.New(client)
	adminSERVICE := adminService.New(adminREPO)
	adminkaUSECASE := adminka.New(s, p, a)

	// Админ
	if cfg.MigratePostgres {
		adminMskKote := &admin.DTO{
			Name:     cfg.Adminka.Username,
			Password: cfg.Adminka.Password,
		}

		logger.Info(fmt.Sprintf("[ADMINKA] Админка: {%s}, {%s}", adminMskKote.Name, adminMskKote.Password))

		if err := adminSERVICE.Create(context.Background(), adminMskKote); err != nil {
			logger.Fatal("[ADMINKA] Не смогли создать админа: "+adminMskKote.Name, zap.Error(err))
		} else {
			logger.Info(fmt.Sprintf("[ADMINKA] Админка: {%s}, {%s}", adminMskKote.Name, adminMskKote.Password))
		}
	}

	auth := security.Startup(adminSERVICE)

	adminkaGroup := r.Group("/adminka")
	adminkaGroup.POST("/login", auth.LoginHandler)
	adminkaGroup.OPTIONS("/login")
	adminkaGroup.Use(auth.MiddlewareFunc())
	{
		adminkaGroup.GET("/refresh_token", auth.RefreshHandler)
		adminkaApiV1 := adminkaGroup.Group("api/v1")
		routes.RegisterSourcesRoutes(adminkaApiV1, adminkaUSECASE)
		routes.RegisterPublishersRoutes(adminkaApiV1, adminkaUSECASE)
	}

	r.NoRoute(auth.MiddlewareFunc(), security.NoRoute)
}

func prosperoRoutes(
	r *gin.Engine,
	p *publishersService.IPublishersService,
	a *articleService.IArticleService) {

	searchUSECASE := search.New(p, a)

	apiV1 := r.Group("/api/v1")
	{
		routes.RegisterSearchRoutes(apiV1, searchUSECASE)
	}
}

func serviceRoutes(
	r *gin.Engine) {

	serviceSERVICE := service.New()
	serviceGroup := r.Group("/service")
	{
		routes.RegisterServiceRoutes(serviceGroup, serviceSERVICE)
	}
}
