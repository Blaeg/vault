package vault

import (
	"context"
	"fmt"

	"github.com/golang/protobuf/ptypes"
	memdb "github.com/hashicorp/go-memdb"
	uuid "github.com/hashicorp/go-uuid"
	"github.com/hashicorp/vault/helper/locksutil"
	"github.com/hashicorp/vault/helper/storagepacker"
	"github.com/hashicorp/vault/helper/token"
)

const (
	tokenMappingTable = "token_mappings"
)

func tokenStoreSchema() *memdb.DBSchema {
	tsSchema := &memdb.DBSchema{
		Tables: make(map[string]*memdb.TableSchema),
	}

	schemas := []func() *memdb.TableSchema{
		tokenMappingsTableSchema,
	}

	for _, schemaFunc := range schemas {
		schema := schemaFunc()
		if _, ok := tsSchema.Tables[schema.Name]; ok {
			panic(fmt.Sprintf("duplicate table name: %s", schema.Name))
		}
		tsSchema.Tables[schema.Name] = schema
	}

	return tsSchema
}

func tokenMappingsTableSchema() *memdb.TableSchema {
	return &memdb.TableSchema{
		Name: tokenMappingTable,
		Indexes: map[string]*memdb.IndexSchema{
			"id": &memdb.IndexSchema{
				Name:   "id",
				Unique: true,
				Indexer: &memdb.StringFieldIndex{
					Field: "ID",
				},
			},
			"token_id": &memdb.IndexSchema{
				Name:   "token_id",
				Unique: true,
				Indexer: &memdb.StringFieldIndex{
					Field: "TokenID",
				},
			},
			"accessor": &memdb.IndexSchema{
				Name:   "accessor",
				Unique: true,
				Indexer: &memdb.StringFieldIndex{
					Field: "Accessor",
				},
			},
			"parent_id": &memdb.IndexSchema{
				Name:         "parent_id",
				Unique:       false,
				AllowMissing: true,
				Indexer: &memdb.StringFieldIndex{
					Field: "ParentID",
				},
			},
		},
	}
}

func (c *Core) loadTokenStoreArtifacts(ctx context.Context) error {
	var err error
	if c.tokenStore == nil {
		return fmt.Errorf("token store is not setup")
	}

	err = c.tokenStore.loadTokenMappings(ctx)
	if err != nil {
		return err
	}

	return nil
}

func (ts *TokenStore) parseTokenMappingFromBucketItem(item *storagepacker.Item) (*token.TokenMapping, error) {
	if item == nil {
		return nil, fmt.Errorf("nil item")
	}

	var tokenMapping token.TokenMapping
	err := ptypes.UnmarshalAny(item.Message, &tokenMapping)
	if err != nil {
		return nil, fmt.Errorf("failed to decode token mapping from storage bucket item: %v", err)
	}

	return &tokenMapping, nil
}

func (ts *TokenStore) loadTokenMappings(ctx context.Context) error {
	ts.logger.Debug("token: loading token mappings")
	existing, err := ts.mappingPacker.View().List(ctx, tokenMappingBucketsPrefix)
	if err != nil {
		return fmt.Errorf("failed to scan for token mappings: %v", err)
	}
	ts.logger.Debug("token: token mappings collected", "num_existing", len(existing))

	for _, key := range existing {
		bucket, err := ts.mappingPacker.GetBucket(ts.mappingPacker.BucketPath(key))
		if err != nil {
			return err
		}

		if bucket == nil {
			continue
		}

		for _, item := range bucket.Items {
			tokenMapping, err := ts.parseTokenMappingFromBucketItem(item)
			if err != nil {
				return err
			}
			if tokenMapping == nil {
				continue
			}

			txn := ts.db.Txn(true)

			err = ts.UpsertTokenMappingInTxn(txn, tokenMapping, false)
			if err != nil {
				txn.Abort()
				return fmt.Errorf("failed to update token mapping in memdb: %v", err)
			}

			txn.Commit()
		}
	}

	if ts.logger.IsInfo() {
		ts.logger.Info("token: groups restored")
	}

	return nil
}

func (ts *TokenStore) CreateTokenMapping(te *TokenEntry, persist bool) (*token.TokenMapping, error) {
	id, err := uuid.GenerateUUID()
	if err != nil {
		return nil, err
	}

	tokenMapping := &token.TokenMapping{
		ID:       id,
		TokenID:  te.ID,
		Accessor: te.Accessor,
		ParentID: te.Parent,
	}

	err = ts.UpsertTokenMapping(tokenMapping, persist)
	if err != nil {
		return nil, err
	}

	return tokenMapping, nil
}

func (ts *TokenStore) UpsertTokenMapping(tokenMapping *token.TokenMapping, persist bool) error {
	txn := ts.db.Txn(true)
	defer txn.Abort()

	err := ts.UpsertTokenMappingInTxn(txn, tokenMapping, persist)
	if err != nil {
		return err
	}

	txn.Commit()

	return nil
}

func (ts *TokenStore) UpsertTokenMappingInTxn(txn *memdb.Txn, tokenMapping *token.TokenMapping, persist bool) error {
	var err error

	lock := locksutil.LockForKey(ts.mappingLocks, tokenMapping.ID)
	lock.Lock()
	defer lock.Unlock()

	err = ts.MemDBUpsertTokenMappingInTxn(txn, tokenMapping)
	if err != nil {
		return err
	}

	if !persist {
		return nil
	}

	tokenMappingAsAny, err := ptypes.MarshalAny(tokenMapping)
	if err != nil {
		return err
	}
	item := &storagepacker.Item{
		ID:      tokenMapping.ID,
		Message: tokenMappingAsAny,
	}

	err = ts.mappingPacker.PutItem(item)
	if err != nil {
		return err
	}

	return nil
}

func (ts *TokenStore) MemDBUpsertTokenMapping(tokenMapping *token.TokenMapping) error {
	if tokenMapping == nil {
		return fmt.Errorf("token mapping is nil")
	}

	txn := ts.db.Txn(true)
	defer txn.Abort()

	err := ts.MemDBUpsertTokenMappingInTxn(txn, tokenMapping)
	if err != nil {
		return err
	}

	txn.Commit()

	return nil
}

func (ts *TokenStore) MemDBUpsertTokenMappingInTxn(txn *memdb.Txn, tokenMapping *token.TokenMapping) error {
	if txn == nil {
		return fmt.Errorf("nil txn")
	}

	if tokenMapping == nil {
		return fmt.Errorf("token mapping is nil")
	}

	tokenMappingRaw, err := txn.First(tokenMappingTable, "id", tokenMapping.ID)
	if err != nil {
		return fmt.Errorf("failed to lookup token mapping from memdb using token mapping ID: %v", err)
	}

	if tokenMappingRaw != nil {
		err = txn.Delete(tokenMappingTable, tokenMappingRaw)
		if err != nil {
			return fmt.Errorf("failed to delete token mapping from memdb: %v", err)
		}
	}

	if err := txn.Insert(tokenMappingTable, tokenMapping); err != nil {
		return fmt.Errorf("failed to update token mapping into memdb: %v", err)
	}

	return nil
}

func (ts *TokenStore) MemDBTokenMappingByTokenID(tokenID string, clone bool) (*token.TokenMapping, error) {
	if tokenID == "" {
		return nil, fmt.Errorf("missing token id")
	}

	txn := ts.db.Txn(false)

	return ts.MemDBTokenMappingByTokenIDInTxn(txn, tokenID, clone)
}

func (ts *TokenStore) MemDBTokenMappingByTokenIDInTxn(txn *memdb.Txn, tokenID string, clone bool) (*token.TokenMapping, error) {
	if tokenID == "" {
		return nil, fmt.Errorf("missing token id")
	}

	if txn == nil {
		return nil, fmt.Errorf("txn is nil")
	}

	tmRaw, err := txn.First(tokenMappingTable, "token_id", tokenID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch token mapping from memdb using token ID: %v", err)
	}

	if tmRaw == nil {
		return nil, nil
	}

	tm, ok := tmRaw.(*token.TokenMapping)
	if !ok {
		return nil, fmt.Errorf("failed to declare the type of fetched token mapping")
	}

	if clone {
		return tm.Clone()
	}

	return tm, nil
}

func (ts *TokenStore) MemDBTokenMappingByAccessor(accessor string, clone bool) (*token.TokenMapping, error) {
	if accessor == "" {
		return nil, fmt.Errorf("missing accessor")
	}

	txn := ts.db.Txn(false)

	return ts.MemDBTokenMappingByAccessorInTxn(txn, accessor, clone)
}

func (ts *TokenStore) MemDBTokenMappingByAccessorInTxn(txn *memdb.Txn, accessor string, clone bool) (*token.TokenMapping, error) {
	if accessor == "" {
		return nil, fmt.Errorf("missing accessor")
	}

	if txn == nil {
		return nil, fmt.Errorf("txn is nil")
	}

	tmRaw, err := txn.First(tokenMappingTable, "accessor", accessor)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch token mapping from memdb using accessor: %v", err)
	}

	if tmRaw == nil {
		return nil, nil
	}

	tm, ok := tmRaw.(*token.TokenMapping)
	if !ok {
		return nil, fmt.Errorf("failed to declare the type of fetched token mapping")
	}

	if clone {
		return tm.Clone()
	}

	return tm, nil
}

func (ts *TokenStore) MemDBTokenMappingsByParentID(parentID string, clone bool) ([]*token.TokenMapping, error) {
	if parentID == "" {
		return nil, fmt.Errorf("empty parent id")
	}

	txn := ts.db.Txn(false)
	defer txn.Abort()

	return ts.MemDBTokenMappingsByParentIDInTxn(txn, parentID, clone)
}

func (ts *TokenStore) MemDBTokenMappingsByParentIDInTxn(txn *memdb.Txn, parentID string, clone bool) ([]*token.TokenMapping, error) {
	if txn == nil {
		return nil, fmt.Errorf("nil txn")
	}

	if parentID == "" {
		return nil, fmt.Errorf("empty parent id")
	}

	tmIter, err := txn.Get(tokenMappingTable, "parent_id", parentID)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup token mappings using parent id: %v", err)
	}

	var tokenMappings []*token.TokenMapping
	for tokenMapping := tmIter.Next(); tokenMapping != nil; tokenMapping = tmIter.Next() {
		entry := tokenMapping.(*token.TokenMapping)
		if clone {
			entry, err = entry.Clone()
			if err != nil {
				return nil, err
			}
		}
		tokenMappings = append(tokenMappings, entry)
	}

	return tokenMappings, nil
}

func (ts *TokenStore) DeleteTokenMappingByTokenID(tokenID string) error {
	txn := ts.db.Txn(true)
	defer txn.Abort()

	tokenMapping, err := ts.MemDBTokenMappingByTokenIDInTxn(txn, tokenID, false)
	if err != nil {
		return err
	}

	lock := locksutil.LockForKey(ts.mappingLocks, tokenMapping.ID)
	lock.Lock()
	defer lock.Unlock()

	// Delete the mapping in MemDB
	err = ts.MemDBDeleteTokenMappingByTokenIDInTxn(txn, tokenID)
	if err != nil {
		return err
	}

	// Delete the mapping from storage
	err = ts.mappingPacker.DeleteItem(tokenMapping.ID)
	if err != nil {
		return err
	}

	// Commit after deleting in both MemDB and storage
	txn.Commit()

	return nil
}

func (ts *TokenStore) MemDBDeleteTokenMappingByTokenIDInTxn(txn *memdb.Txn, tokenID string) error {
	if tokenID == "" {
		return nil
	}

	if txn == nil {
		return fmt.Errorf("txn is nil")
	}

	tokenMapping, err := ts.MemDBTokenMappingByTokenIDInTxn(txn, tokenID, false)
	if err != nil {
		return err
	}

	if tokenMapping == nil {
		return nil
	}

	err = txn.Delete(tokenMappingTable, tokenMapping)
	if err != nil {
		return fmt.Errorf("failed to delete token mapping from memdb: %v", err)
	}

	return nil
}

func (ts *TokenStore) MemDBTokenMappings(clone bool) ([]*token.TokenMapping, error) {
	txn := ts.db.Txn(false)

	tmIter, err := txn.Get(tokenMappingTable, "id")
	if err != nil {
		return nil, err
	}

	var tokenMappings []*token.TokenMapping
	for tokenMapping := tmIter.Next(); tokenMapping != nil; tokenMapping = tmIter.Next() {
		entry := tokenMapping.(*token.TokenMapping)
		if clone {
			entry, err = entry.Clone()
			if err != nil {
				return nil, err
			}
		}
		tokenMappings = append(tokenMappings, entry)
	}

	return tokenMappings, nil
}
