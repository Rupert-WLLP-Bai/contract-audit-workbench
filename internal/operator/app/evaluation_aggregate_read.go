package app

import (
	"context"
	"time"

	"contractaudit/internal/operator/model"
)

type MappingPin struct {
	BindingVersion, MappingID string
	MappingVersion            int64
}
type MappingPinCursor interface {
	Next() bool
	ScanPin() (MappingPin, error)
	Err() error
	Close()
}
type AggregateRepository interface {
	MappingRepository
	CollectionRepository
	MappingPins(context.Context, ReadIdentity, string, time.Time, time.Time) (MappingPinCursor, error)
	SnapshotIDAt(context.Context, string, string, time.Time, time.Time) (string, bool, error)
}

func ReadApplicableMappings(ctx context.Context, repo AggregateRepository, id ReadIdentity, object ServiceObjectAtTime, at, cutoff time.Time) ([]model.OperatorMappingVersion, error) {
	rows, err := repo.MappingPins(ctx, id, object.Head.ID, at, cutoff)
	if err != nil {
		return nil, err
	}
	var pins []MappingPin
	for rows.Next() {
		v, e := rows.ScanPin()
		if e != nil {
			rows.Close()
			return nil, e
		}
		pins = append(pins, v)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]model.OperatorMappingVersion, 0, len(pins))
	for _, p := range pins {
		binding, e := ReadBindingVersion(ctx, repo, id, p.BindingVersion)
		if e != nil {
			return nil, e
		}
		v := binding.Value
		if v.ServiceObjectID != object.Head.ID || v.ObjectVersion != object.Version.Version || v.AssociationVersion != object.Association.AssociationVersion {
			continue
		}
		out = append(out, model.OperatorMappingVersion{MappingID: p.MappingID, MappingVersion: p.MappingVersion, ServiceObjectID: object.Head.ID, ObjectVersion: object.Version.Version, AssociationVersion: object.Association.AssociationVersion, Binding: v})
	}
	return out, nil
}
func ReadAggregateSnapshot(ctx context.Context, repo AggregateRepository, caseID, bindingVersion string, businessAt time.Time, cutoff *time.Time) (model.OperatorSnapshotSet, bool, error) {
	cut := businessAt
	if cutoff != nil {
		cut = *cutoff
	}
	id, found, err := repo.SnapshotIDAt(ctx, caseID, bindingVersion, businessAt, cut)
	if err != nil || !found {
		return model.OperatorSnapshotSet{}, false, err
	}
	v, err := ReadCollectionSnapshot(ctx, repo, caseID, id)
	return v, err == nil, err
}

type ObjectHeadCursor interface {
	Next() bool
	ScanHead() (model.ServiceObjectHead, error)
	Err() error
	Close()
}
type AggregateObjectRepository interface {
	ReadRepository
	ObjectHeads(context.Context, ReadIdentity) (ObjectHeadCursor, error)
	VersionAt(context.Context, ReadIdentity, string, time.Time, time.Time) (int, bool, error)
	AssociationAt(context.Context, ReadIdentity, string, int, time.Time) (int, bool, error)
}

func ReadServiceObjectsAt(ctx context.Context, repo AggregateObjectRepository, id ReadIdentity, at, cutoff time.Time) ([]ServiceObjectAtTime, error) {
	rows, err := repo.ObjectHeads(ctx, id)
	if err != nil {
		return nil, err
	}
	var heads []model.ServiceObjectHead
	for rows.Next() {
		h, e := rows.ScanHead()
		if e != nil {
			rows.Close()
			return nil, e
		}
		heads = append(heads, h)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out := make([]ServiceObjectAtTime, 0, len(heads))
	for _, h := range heads {
		number, found, e := repo.VersionAt(ctx, id, h.ID, at, cutoff)
		if e != nil {
			return nil, e
		}
		if !found {
			continue
		}
		version, e := ReadServiceObjectVersionExact(ctx, repo, id, h.ID, number)
		if e != nil {
			return nil, e
		}
		assocNumber, found, e := repo.AssociationAt(ctx, id, h.ID, number, cutoff)
		if e != nil {
			return nil, e
		}
		var association *model.ServiceObjectContractAssociation
		if found {
			v, e := ReadServiceObjectAssociationExact(ctx, repo, id, h.ID, assocNumber)
			if e != nil {
				return nil, e
			}
			association = &v
		}
		out = append(out, ServiceObjectAtTime{Head: h, Version: version, Association: association})
	}
	return out, nil
}
