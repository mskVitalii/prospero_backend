package publishersRepository

import (
	"context"
	"github.com/mskKote/prospero_backend/internal/domain/entity/publisher"
)

type IRepository interface {
	Create(ctx context.Context, source *publisher.Publisher) (*publisher.Publisher, error)
	FindAll(ctx context.Context) (u []*publisher.Publisher, err error)
	FindPublishersByName(ctx context.Context, name string) ([]*publisher.Publisher, error)
	Update(ctx context.Context, publisher *publisher.Publisher) error
	Delete(ctx context.Context, id string) error
}