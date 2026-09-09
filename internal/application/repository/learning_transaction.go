package repository

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type learningTransactionKey struct{}
type learningTransaction struct {
	owner   *learningRepository
	db      *gorm.DB
	subject string
}

func (r *learningRepository) database(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(learningTransactionKey{}).(*learningTransaction); ok && tx.owner == r {
		return tx.db.WithContext(ctx)
	}
	return r.db.WithContext(ctx)
}

// Insert first: SELECT FOR UPDATE cannot lock a row which does not exist.
// Every personal operation takes this lock BEFORE touching personal tables.
// On SQLite the insert obtains the writer reservation before any reads.
func lockLearningSubject(tx *gorm.DB, subject string) (*types.LearningSubjectEpoch, error) {
	if subject == "" {
		return nil, errors.New("learning: empty subject")
	}
	row := types.LearningSubjectEpoch{SubjectID: subject, UpdatedAt: time.Now()}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	q := tx.Where("subject_id = ?", subject)
	if tx.Dialector.Name() == "postgres" {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err := q.First(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *learningRepository) WithSubject(ctx context.Context, subject string, epoch int64, collect bool, fn func(context.Context) error) error {
	if active, ok := ctx.Value(learningTransactionKey{}).(*learningTransaction); ok && active.owner == r {
		if active.subject != subject {
			return errors.New("learning: cross-subject nested transaction")
		}
		row, err := lockLearningSubject(active.db.WithContext(ctx), subject)
		if err != nil {
			return err
		}
		if row.Epoch != epoch {
			return interfaces.ErrLearningEpochAdvanced
		}
		if collect {
			prefs, err := r.GetSubjectPrefs(ctx, subject)
			if err != nil {
				return err
			}
			if prefs != nil && prefs.CollectDisabled {
				return interfaces.ErrLearningCollectionDisabled
			}
		}
		return fn(ctx)
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := lockLearningSubject(tx, subject)
		if err != nil {
			return err
		}
		if row.Epoch != epoch {
			return interfaces.ErrLearningEpochAdvanced
		}
		txCtx := context.WithValue(ctx, learningTransactionKey{}, &learningTransaction{r, tx, subject})
		if collect {
			prefs, err := r.GetSubjectPrefs(txCtx, subject)
			if err != nil {
				return err
			}
			if prefs != nil && prefs.CollectDisabled {
				return interfaces.ErrLearningCollectionDisabled
			}
		}
		return fn(txCtx)
	})
}

func (r *learningRepository) ListEventScopes(ctx context.Context) ([]interfaces.LearningScope, error) {
	var scopes []interfaces.LearningScope
	err := r.database(ctx).Model(&types.LearningEvent{}).
		Distinct("tenant_id", "subject_id", "knowledge_base_id").
		Order("tenant_id, subject_id, knowledge_base_id").Scan(&scopes).Error
	return scopes, err
}
