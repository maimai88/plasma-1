package db

import (
	"fmt"
	"github.com/kyokan/plasma/merkle"
	"github.com/syndtr/goleveldb/leveldb/iterator"
		"math/big"
	"strconv"
	"strings"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/kyokan/plasma/chain"
	"github.com/kyokan/plasma/util"
	"github.com/pkg/errors"
	"github.com/syndtr/goleveldb/leveldb"
	levelutil "github.com/syndtr/goleveldb/leveldb/util"
	"time"
)

const FeeTxIdx = 65535 // 2^16 - 1

type BlockResult struct {
	MerkleRoot         util.Hash
	NumberTransactions uint32
	BlockFees          *big.Int
	BlockNumber        *big.Int
}

type PlasmaStorage interface {
	ProcessDeposit(tx chain.ConfirmedTransaction) (deposit *BlockResult, err error)
	FindTransactionsByBlockNum(blkNum uint64) ([]chain.ConfirmedTransaction, error)
	FindTransactionByBlockNumTxIdx(blkNum uint64, txIdx uint32) (*chain.ConfirmedTransaction, error)

	Balance(addr *common.Address) (*big.Int, error)
	SpendableTxs(addr *common.Address) ([]chain.ConfirmedTransaction, error)
	UTXOs(addr *common.Address) ([]chain.ConfirmedTransaction, error)

	BlockAtHeight(num uint64) (*chain.Block, error)
	BlockMetaAtHeight(num uint64) (*chain.BlockMetadata, error)
	LatestBlock() (*chain.Block, error)
	PackageBlock(txs []chain.ConfirmedTransaction) (result *BlockResult, err error)
	ConfirmTransaction(blockNumber uint64, transactionIndex uint32, sigs [2]chain.Signature) (*chain.ConfirmedTransaction, error)
	AuthSigsFor(blockNumber uint64, transactionIndex uint32) ([2]chain.Signature, error)

	LastDepositPoll() (uint64, error)
	SaveDepositPoll(idx uint64) error

	LastTxExitPoll() (uint64, error)
	SaveTxExitPoll(idx uint64) error

	LastDepositExitEventIdx() (uint64, error)
	SaveDepositExitEventIdx(idx uint64) error

	MarkExitsAsSpent([]chain.Input) error

	IsDoubleSpent(tx *chain.ConfirmedTransaction) (bool, error)

	SaveLastSubmittedBlock(num uint64) error
	LastSubmittedBlock() (uint64, error)

	FindDoubleSpendingTransaction(blkNum uint64, txIdx uint32, outIndex uint8) (*chain.ConfirmedTransaction, error)
}

type Storage struct {
	db *leveldb.DB
}

func NewStorage(db *leveldb.DB) PlasmaStorage {
	result := Storage{
		db: db,
	}

	return &result
}

func (ps *Storage) Put(key, value []byte) {
	ps.db.Put(key, value, nil)
}

func (ps *Storage) Delete(key []byte) {
	ps.db.Delete(key, nil)
}

func (ps *Storage) findPreviousTx(tx *chain.ConfirmedTransaction, inputIdx uint8) (*chain.ConfirmedTransaction, util.Hash, error) {
	var input *chain.Input

	if inputIdx != 0 && inputIdx != 1 {
		panic("inputIdx must be 0 or 1")
	}

	if inputIdx == 0 {
		input = tx.Transaction.Input0
	} else {
		input = tx.Transaction.Input1
	}

	return ps.findTransactionByBlockNumTxIdx(input.BlkNum, input.TxIdx)
}

func (ps *Storage) saveTransaction(blkNum uint64, txIdx uint32, confirmed chain.ConfirmedTransaction, batch *leveldb.Batch) (*chain.ConfirmedTransaction, error) {
	confirmed.Transaction.TxIdx = txIdx
	confirmed.Transaction.BlkNum = blkNum

	txEnc, err := rlp.EncodeToBytes(&confirmed)
	if err != nil {
		return nil, err
	}

	hash := confirmed.RLPHash(util.Sha256)
	hexHash := hexutil.Encode(hash)
	hashKey := txPrefixKey("hash", hexHash)

	batch.Put(hashKey, txEnc)
	batch.Put(blkNumHashkey(confirmed.Transaction.BlkNum, hexHash), txEnc)
	batch.Put(blkNumTxIdxKey(confirmed.Transaction.BlkNum, confirmed.Transaction.TxIdx), txEnc)

	var empty []byte

	// Recording spends
	if !confirmed.Transaction.Input0.IsZeroInput() {
		outpointIdent := &chain.SpendIdentifier{
			BlockNumber: blkNum,
			TransactionIndex:txIdx,
			InputIndex: 1,
		}
		identBytes, _ := outpointIdent.MarshalBinary()

		prevTx0, _, err := ps.findPreviousTx(&confirmed, 0)
		if err != nil {
			return nil, err
		}

		input := confirmed.Transaction.Input0
		prevOutput := prevTx0.Transaction.OutputAt(input.OutIdx)
		outputOwner := prevOutput.Owner
		if confirmed.Transaction.Output0.IsExit() {
			batch.Put(spendExit(&outputOwner, confirmed.Transaction.Input0), identBytes)
		} else {
			batch.Put(spend(&outputOwner, confirmed.Transaction.Input0), identBytes)
		}
	}
	if !confirmed.Transaction.Input1.IsZeroInput() {
		outpointIdent := &chain.SpendIdentifier{
			BlockNumber: blkNum,
			TransactionIndex:txIdx,
			InputIndex: 1,
		}
		identBytes, _ := outpointIdent.MarshalBinary()

		prevTx1, _, err := ps.findPreviousTx(&confirmed, 1)
		if err != nil {
			return nil, err
		}
		input := confirmed.Transaction.InputAt(1)
		outputOwner := prevTx1.Transaction.OutputAt(input.OutIdx).Owner
		batch.Put(spend(&outputOwner, confirmed.Transaction.Input1), identBytes)
	}

	// Recording earns
	if !confirmed.Transaction.Output0.IsZeroOutput() {
		if confirmed.Transaction.Output0.IsDeposit() { // Only first output can be a deposit
			batch.Put(depositKey(&confirmed), txEnc)
		}
		output := confirmed.Transaction.OutputAt(0)
		batch.Put(earn(&output.Owner, confirmed, 0), empty)
	}
	if !confirmed.Transaction.Output1.IsZeroOutput() {
		output := confirmed.Transaction.OutputAt(1)
		batch.Put(earn(&output.Owner, confirmed, 1), empty)
	}

	return &confirmed, batch.Replay(ps)
}

func (ps *Storage) MarkExitsAsSpent(inputs []chain.Input) error {
	//for _, input := range inputs {
	//	if input.TxIdx.Cmp(big.NewInt(FeeTxIdx)) == 0 { // fee exit
	//		blkNum := input.BlkNum.Uint64()
	//		err := ps.db.Put(blockFeesExitKey(blkNum), input.Denom.Bytes(), nil)
	//		if err != nil {
	//			log.Printf("Error: Failed to exit fees for block %v", blkNum)
	//		}
	//		continue
	//	}
	//	confirmed := chain.ConfirmedTransaction{
	//		Transaction: *chain.ZeroTransaction(),
	//	}
	//	confirmed.Transaction.Input0 = &input
	//	confirmed.Transaction.Output0 = chain.ExitOutput()
	//	var prevTx *chain.ConfirmedTransaction
	//	var err error
	//	if input.IsDeposit() { // this is a deposit exit
	//		prevTx, _, err = ps.findTransactionByDepositNonce(input.DepositNonce, noopLock{})
	//	} else {
	//		prevTx, _, err = ps.findTransactionByBlockNumTxIdx(input.BlkNum.Uint64(), uint32(input.TxIdx.Uint64()), noopLock{})
	//	}
	//	if err != nil {
	//		log.Printf("Failed to find previous transaction(s) for exit: %s", err)
	//		continue
	//	}
	//
	//	//ps.saveTransaction(confirmed, []*chain.ConfirmedTransaction{prevTx})
	//}

	return nil
}

func (ps *Storage) IsDoubleSpent(confirmed *chain.ConfirmedTransaction) (bool, error) {
	tx := confirmed.Transaction
	prevTx0, _, err := ps.findPreviousTx(confirmed, 0)
	if err != nil {
		return false, err
	}
	spendKeys := make([][]byte, 0)
	addr0 := prevTx0.Transaction.OutputAt(tx.Input0.OutIdx).Owner
	spendKeys = append(spendKeys, spend(&addr0, tx.Input0))
	spendKeys = append(spendKeys, spendExit(&addr0, tx.Input0))
	if !tx.Input1.IsZeroInput() {
		prevTx1, _, err := ps.findPreviousTx(confirmed, 1)
		if err != nil {
			return false, err
		}
		addr1 := prevTx1.Transaction.OutputAt(tx.Input1.OutIdx).Owner
		spendKeys = append(spendKeys, spend(&addr1, tx.Input1))
		spendKeys = append(spendKeys, spendExit(&addr1, tx.Input1))
	}

	for _, spendKey := range spendKeys {
		found, _ := ps.db.Has(spendKey, nil)
		if found {
			return true, nil
		}
	}

	return false, nil
}

func (ps *Storage) FindDoubleSpendingTransaction(blkNum uint64, txIdx uint32, outIndex uint8) (*chain.ConfirmedTransaction, error) {
	confirmed, _, err := ps.findTransactionByBlockNumTxIdx(blkNum, txIdx)
	if err != nil {
		return nil, err
	}

	tx := confirmed.Transaction
	spendKeys := make([][]byte, 0)
	addr := tx.OutputAt(outIndex).Owner
	spendKeys = append(spendKeys, rawSpend(&addr, blkNum, txIdx, outIndex, big.NewInt(0)))
	spendKeys = append(spendKeys, spendExit(&addr, tx.Input0))

	var spendIdent *chain.SpendIdentifier
	for _, spendKey := range spendKeys {
		found, _ := ps.db.Has(spendKey, nil)
		if !found {
			continue
		}

		identBinary, err := ps.db.Get(spendKey, nil)
		if err != nil {
			return nil, err
		}
		var ident chain.SpendIdentifier
		if err = ident.UnmarshalBinary(identBinary); err != nil {
		    return nil, err
		}
		spendIdent = &ident
		break
	}

	if spendIdent == nil {
		return nil, nil
	}

	spendingTx, _, err := ps.findTransactionByBlockNumTxIdx(spendIdent.BlockNumber, spendIdent.TransactionIndex)
	if err != nil {
		return nil, err
	}
	return spendingTx, nil
}

func (ps *Storage) doPackageBlock(txs []chain.ConfirmedTransaction) (*BlockResult, error) {
	prevBlock, err := ps.LatestBlock()
	if err != nil {
		return nil, err
	}

	var blkNum uint64
	var prevHash util.Hash
	if prevBlock == nil {
		blkNum = 1
	} else {
		blkNum = prevBlock.Header.Number + 1
		prevHash = prevBlock.BlockHash
	}

	// The batch will act as in-memory buffer
	batch := new(leveldb.Batch)
	numberOfTransactions := len(txs)

	hashables := make([]util.RLPHashable, numberOfTransactions)
	for i, tx := range txs {
		hashables[i] = &tx
	}
	merkleRoot := merkle.Root(hashables)

	header := chain.BlockHeader{
		MerkleRoot: merkleRoot,
		PrevHash:   prevHash,
		Number:     blkNum,
	}
	block := chain.Block{
		Header:    &header,
		BlockHash: header.Hash(),
	}

	enc, err := rlp.EncodeToBytes(merkleRoot)
	if err != nil {
		return nil, err
	}
	batch.Put(merklePrefixKey(hexutil.Encode(merkleRoot)), enc)

	enc, err = rlp.EncodeToBytes(block)
	if err != nil {
		return nil, err
	}
	key := blockPrefixKey(hexutil.Encode(block.BlockHash))
	batch.Put(key, enc)
	batch.Put(blockPrefixKey(latestKey), key)
	batch.Put(blockNumKey(block.Header.Number), key)

	currentFees := big.NewInt(0)
	for i, tx := range txs {
		_, err := ps.saveTransaction(blkNum, uint32(i), tx, batch)
		if err != nil {
			return nil, err
		}

		currentFees = currentFees.Add(currentFees, tx.Transaction.Fee)
	}
	batch.Put(blockFeesKey(blkNum), currentFees.Bytes())

	meta := &chain.BlockMetadata{
		CreatedAt:        uint64(time.Now().Unix()),
		TransactionCount: uint32(numberOfTransactions),
		Fees:             currentFees,
	}
	metaEnc, err := meta.RLP()
	if err != nil {
		return nil, err
	}
	batch.Put(blockMetaPrefixKey(block.Header.Number), metaEnc)

	err = ps.db.Write(batch, nil)
	if err != nil {
		return nil, err
	}

	return &BlockResult{
		MerkleRoot:         merkleRoot,
		NumberTransactions: uint32(numberOfTransactions),
		BlockFees:          currentFees,
		BlockNumber:        big.NewInt(int64(block.Header.Number)),
	}, nil
}

func (ps *Storage) ProcessDeposit(confirmed chain.ConfirmedTransaction) (deposit *BlockResult, err error) {
	if !confirmed.Transaction.IsDeposit() {
		return nil, errors.New("only deposit blocks are accepted")
	}

	return ps.doPackageBlock([]chain.ConfirmedTransaction{confirmed})
}

func (ps *Storage) FindTransactionsByBlockNum(blkNum uint64) ([]chain.ConfirmedTransaction, error) {

	// Construct partial prefix that matches all transactions for the block
	prefix := txPrefixKey("blkNum", strconv.FormatUint(blkNum, 10), "txIdx")
	prefix = append(prefix, ':', ':')

	iter := ps.db.NewIterator(levelutil.BytesPrefix(prefix), nil)
	defer iter.Release()

	return findBlockTransactions(iter, prefix, blkNum)
}

func findBlockTransactions(iter iterator.Iterator, prefix []byte, blkNum uint64) ([]chain.ConfirmedTransaction, error) {
	var buffer []chain.ConfirmedTransaction

	for iter.Next() {
		var tx chain.ConfirmedTransaction
		// Extract transaction index
		// prefix looks like "tx::blkNum::1::txIdx::"
		// key looks like    "tx::blkNum::1::txIdx::20"
		idx := string(iter.Key()[len(prefix):])
		txIdx, ok := util.Str2Uint32(idx)
		if ok == false {
			return nil, errors.New("Failed to parse transaction index from key")
		}
		err := rlp.DecodeBytes(iter.Value(), &tx)
		if err != nil {
			return nil, err
		}
		// RLP encoding for tranctions doesn't contain TxIdx or BlkNum
		tx.Transaction.TxIdx = txIdx
		tx.Transaction.BlkNum = blkNum
		buffer = append(buffer, tx)
	}

	txs := make([]chain.ConfirmedTransaction, len(buffer))
	for _, tx := range buffer {
		txs[tx.Transaction.TxIdx] = tx
	}

	return txs, nil
}

func (ps *Storage) findTransactionByDepositNonce(nonce *big.Int) (*chain.ConfirmedTransaction, util.Hash, error) {
	keyPrefix := depositPrefixKey(nonce)
	iter := ps.db.NewIterator(levelutil.BytesPrefix(keyPrefix), nil)
	defer iter.Release()

	for iter.Next() {
		var confirmed chain.ConfirmedTransaction
		key := string(iter.Key())
		keyParts := strings.Split(key, "::")
		if len(keyParts) != 4 {
			return nil, nil, errors.New(fmt.Sprintf("Failed to parse deposit transaction position from key %s", key))
		}
		blkNum, success := util.Str2Uint64(keyParts[2])
		if success == false {
			return nil, nil, errors.New(fmt.Sprintf("Failed to parse block number from key %s", key))
		}
		txIdx, success := util.Str2Uint32(keyParts[3])
		if success == false {
			return nil, nil, errors.New(fmt.Sprintf("Failed to parse transaction index from key %s", key))
		}
		err := rlp.DecodeBytes(iter.Value(), &confirmed)
		if err != nil {
			return nil, nil, err
		}
		confirmed.Transaction.BlkNum = blkNum
		confirmed.Transaction.TxIdx = txIdx
		return &confirmed, nil, nil
	}
	return nil, nil, errors.New(fmt.Sprintf("Failed to find deposit for deposit nonce %s", nonce.String()))
}

func (ps *Storage) findTransactionByBlockNumTxIdx(blkNum uint64, txIdx uint32) (*chain.ConfirmedTransaction, util.Hash, error) {
	key := blkNumTxIdxKey(blkNum, txIdx)
	var data []byte
	var err error

	block, err := ps.BlockAtHeight(blkNum)
	if err != nil {
		return nil, nil, err
	}

	exists, err := ps.db.Has(key, nil)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, nil
	}
	data, err = ps.db.Get(key, nil)
	if err != nil {
		return nil, nil, err
	}

	tx := chain.ConfirmedTransaction{}
	err = rlp.DecodeBytes(data, &tx)
	if err != nil {
		return nil, nil, err
	}
	tx.Transaction.BlkNum = blkNum
	tx.Transaction.TxIdx = txIdx

	return &tx, block.BlockHash, nil
}

func (ps *Storage) FindTransactionByBlockNumTxIdx(blkNum uint64, txIdx uint32) (*chain.ConfirmedTransaction, error) {
	tx, _, err := ps.findTransactionByBlockNumTxIdx(blkNum, txIdx)
	return tx, err
}

// Address
func (ps *Storage) Balance(addr *common.Address) (*big.Int, error) {
	txs, err := ps.SpendableTxs(addr)

	if err != nil {
		return nil, err
	}

	total := big.NewInt(0)

	for _, confirmed := range txs {
		total = total.Add(total, extractAmount(&confirmed.Transaction, addr))
	}

	return total, nil
}

func (ps *Storage) SpendableTxs(addr *common.Address) ([]chain.ConfirmedTransaction, error) {
	earnPrefix := earnPrefixKey(addr)
	spendPrefix := spendPrefixKey(addr)

	earnMap := make(map[string]uint8)
	spendMap := make(map[string]uint8)

	earnIter := ps.db.NewIterator(levelutil.BytesPrefix(earnPrefix), nil)
	defer earnIter.Release()

	for earnIter.Next() {
		earnKey := earnIter.Key()
		lookupKey := string(earnKey[len(earnKeyPrefix)+len(keyPartsSeparator):])
		earnMap[lookupKey] = 1
	}

	spendIter := ps.db.NewIterator(levelutil.BytesPrefix(spendPrefix), nil)
	defer spendIter.Release()

	for spendIter.Next() {
		spendKey := spendIter.Key()
		lookupKey := string(spendKey[len(spendKeyPrefix)+len(keyPartsSeparator) : len(spendKey)-3])
		spendMap[lookupKey] = 1
	}

	for k, _ := range spendMap {
		delete(earnMap, k)
	}

	var ret []chain.ConfirmedTransaction
	for key := range earnMap {
		_, blkNum, txIdx, _, err := parseSuffix([]byte(key))
		if err != nil {
			return nil, err
		}
		tx, _, err := ps.findTransactionByBlockNumTxIdx(blkNum.Uint64(), uint32(txIdx.Uint64()))

		if err != nil {
			return nil, err
		}

		ret = append(ret, *tx)
	}
	sortTransactions(ret)
	return ret, nil
}

func (ps *Storage) UTXOs(addr *common.Address) ([]chain.ConfirmedTransaction, error) {
	earnPrefix := earnPrefixKey(addr)
	earnMap := make(map[string]uint8)

	earnIter := ps.db.NewIterator(levelutil.BytesPrefix(earnPrefix), nil)
	defer earnIter.Release()

	for earnIter.Next() {
		earnKey := earnIter.Key()
		lookupKey := string(earnKey[len(earnKeyPrefix)+len(keyPartsSeparator):])
		earnMap[lookupKey] = 1
	}

	var ret []chain.ConfirmedTransaction
	for key := range earnMap {
		_, blkNum, txIdx, _, err := parseSuffix([]byte(key))
		if err != nil {
			return nil, err
		}
		tx, _, err := ps.findTransactionByBlockNumTxIdx(blkNum.Uint64(), uint32(txIdx.Uint64()))

		if err != nil {
			return nil, err
		}

		ret = append(ret, *tx)
	}
	sortTransactions(ret)
	return ret, nil
}

func (ps *Storage) ConfirmTransaction(blockNumber uint64, transactionIndex uint32, sigs [2]chain.Signature) (*chain.ConfirmedTransaction, error) {
	tx, _, err := ps.findTransactionByBlockNumTxIdx(blockNumber, transactionIndex)
	if err != nil {
		return nil, err
	}

	encoded, err := rlp.EncodeToBytes(sigs)
	if err != nil {
		return nil, err
	}

	batch := new(leveldb.Batch)
	batch.Put(blkNumTxIdxAuthSigKey(blockNumber, transactionIndex), encoded)
	if err != nil {
		return nil, err
	}
	err = ps.db.Write(batch, nil)
	if err != nil {
		return nil, err
	}
	return tx, nil
}

func (ps *Storage) AuthSigsFor(blockNumber uint64, transactionIndex uint32) ([2]chain.Signature, error) {
	var sigs [2]chain.Signature
	key := blkNumTxIdxAuthSigKey(blockNumber, transactionIndex)
	has, err := ps.db.Has(key, nil)
	if err != nil {
		return sigs, err
	}
	if !has {
		return sigs, errors.New("no auth sigs found")
	}
	rawSigs, err := ps.db.Get(key, nil)
	if err != nil {
		return sigs, err
	}

	err = rlp.DecodeBytes(rawSigs, &sigs)
	return sigs, err
}

// Block
func (ps *Storage) BlockAtHeight(num uint64) (*chain.Block, error) {
	key, err := ps.db.Get(blockNumKey(num), nil)
	if err != nil {
		return nil, err
	}
	data, err := ps.db.Get(key, nil)
	if err != nil {
		return nil, err
	}

	var blk chain.Block
	err = rlp.DecodeBytes(data, &blk)

	if err != nil {
		return nil, err
	}

	return &blk, nil
}

func (ps *Storage) BlockMetaAtHeight(num uint64) (*chain.BlockMetadata, error) {
	data, err := ps.db.Get(blockMetaPrefixKey(num), nil)
	if err != nil {
		return nil, err
	}

	var meta chain.BlockMetadata
	err = meta.FromRLP(data)
	if err != nil {
		return nil, err
	}

	return &meta, nil
}

func (ps *Storage) LatestBlock() (*chain.Block, error) {
	key := blockPrefixKey(latestKey)

	exists, err := ps.db.Has(key, nil)

	if err != nil {
		return nil, err
	}

	if !exists {
		return nil, nil
	}

	topKey, err := ps.db.Get(key, nil)
	if err != nil {
		return nil, err
	}
	data, err := ps.db.Get(topKey, nil)
	if err != nil {
		return nil, err
	}

	var blk chain.Block
	err = rlp.DecodeBytes(data, &blk)

	if err != nil {
		return nil, err
	}

	return &blk, nil
}

func (ps *Storage) PackageBlock(txs []chain.ConfirmedTransaction) (*BlockResult, error) {
	return ps.doPackageBlock(txs)
}

// Deposit
func (ps *Storage) LastDepositPoll() (uint64, error) {
	res, err := ps.getMostRecentEventIdx(latestDepositIdxKey)
	if err != nil {
		if err == leveldb.ErrNotFound {
			return 0, nil
		}

		return 0, err
	}

	return res, nil
}

func (ps *Storage) SaveDepositPoll(idx uint64) error {
	return ps.saveEventIdx(latestDepositIdxKey, idx)
}

// Exits
func (ps *Storage) LastTxExitPoll() (uint64, error) {
	return ps.getMostRecentEventIdx(lastTxExitPollKey)
}

func (ps *Storage) SaveTxExitPoll(idx uint64) error {
	return ps.saveEventIdx(lastTxExitPollKey, idx)
}

func (ps *Storage) LastDepositExitEventIdx() (uint64, error) {
	return ps.getMostRecentEventIdx(latestDepExitIdxKey)
}

func (ps *Storage) SaveDepositExitEventIdx(idx uint64) error {
	return ps.saveEventIdx(latestDepExitIdxKey, idx)
}

func (ps *Storage) SaveLastSubmittedBlock(num uint64) error {
	return ps.saveEventIdx(lastSubmittedBlockKey, num)
}

func (ps *Storage) LastSubmittedBlock() (uint64, error) {
	return ps.getMostRecentEventIdx(lastSubmittedBlockKey)
}

func (ps *Storage) saveEventIdx(eventKey string, idx uint64) error {
	key := prefixKey(eventKey)
	b := uint64ToBytes(idx)
	return ps.db.Put(key, b, nil)
}

func (ps *Storage) getMostRecentEventIdx(eventKey string) (uint64, error) {
	key := prefixKey(eventKey)
	b, err := ps.db.Get(key, nil)
	if err == leveldb.ErrNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return bytesToUint64(b), nil
}
