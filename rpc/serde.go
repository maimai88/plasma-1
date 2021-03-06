package rpc

import (
	"encoding/hex"
	"github.com/kyokan/plasma/rpc/pb"
	"math/big"
	"github.com/kyokan/plasma/chain"
	"github.com/ethereum/go-ethereum/common"
	"strings"
	"fmt"
	"github.com/kyokan/plasma/util"
)

func SerializeBig(in *big.Int) (*pb.BigInt) {
	return &pb.BigInt{
		Hex: fmt.Sprintf("0x%s", strings.ToLower(in.Text(16))),
	}
}

func DeserializeBig(in *pb.BigInt) (*big.Int) {
	s := hex.EncodeToString(common.FromHex(in.Hex)) // Ox trips big.Int.SetString
	if len(s) == 0 {
		return big.NewInt(0)
	}
	b, _ := new(big.Int).SetString(s, 16)
	return b
}

func SerializeConfirmedTxs(confirmedTransactions []chain.ConfirmedTransaction) ([]*pb.ConfirmedTransaction) {
	out := make([]*pb.ConfirmedTransaction, len(confirmedTransactions), len(confirmedTransactions))
	for i, confirmed := range confirmedTransactions {
		out[i] = SerializeConfirmedTx(&confirmed)
	}
	return out
}

func DeserializeConfirmedTxs(confirmedTransactions []*pb.ConfirmedTransaction) ([]chain.ConfirmedTransaction) {
	out := make([]chain.ConfirmedTransaction, len(confirmedTransactions), len(confirmedTransactions))
	for i, confirmed := range confirmedTransactions {
		out[i] = *DeserializeConfirmedTx(confirmed)
	}
	return out
}

func SerializeTx(tx *chain.Transaction) (*pb.Transaction) {
	return &pb.Transaction{
		Input0:   SerializeInput(tx.Input0),
		Sig0:     tx.Sig0[:],
		Input1:   SerializeInput(tx.Input1),
		Sig1:     tx.Sig1[:],
		Output0:  SerializeOutput(tx.Output0),
		Output1:  SerializeOutput(tx.Output1),
		Fee:      SerializeBig(tx.Fee),
		BlockNum: tx.BlkNum,
		TxIdx:    tx.TxIdx,
	}
}

func DeserializeTx(tx *pb.Transaction) (*chain.Transaction) {
	result := chain.ZeroTransaction()
	if tx != nil {
		result.Input0 = DeserializeInput(tx.Input0)
		copy(result.Sig0[:], tx.Sig0)
		result.Input1 = DeserializeInput(tx.Input1)
		copy(result.Sig1[:], tx.Sig1)
		result.Output0 = DeserializeOutput(tx.Output0)
		result.Output1 = DeserializeOutput(tx.Output1)
		result.Fee = DeserializeBig(tx.Fee)
		result.BlkNum = tx.BlockNum
		result.TxIdx = tx.TxIdx
		copy(result.Sig0[:], tx.Sig0)
	}
	return result
}

func SerializeConfirmedTx(confirmed *chain.ConfirmedTransaction) (*pb.ConfirmedTransaction) {
	result := &pb.ConfirmedTransaction{
		Transaction: SerializeTx(&confirmed.Transaction),
	}
	result.Signatures = make([][]byte, 2)
	result.Signatures[0] = append(result.Signatures[0], confirmed.Signatures[0][:]...)
	result.Signatures[1] = append(result.Signatures[1], confirmed.Signatures[1][:]...)

	return result;
}

func DeserializeConfirmedTx(confirmed *pb.ConfirmedTransaction) (*chain.ConfirmedTransaction) {
	result := &chain.ConfirmedTransaction{
		Transaction: *DeserializeTx(confirmed.Transaction),
	}
	copy(result.Signatures[0][:], confirmed.Signatures[0][0:65])
	if len(confirmed.Signatures) > 1 {
		copy(result.Signatures[1][:], confirmed.Signatures[1][0:65])
	}
	return result;
}

func SerializeInput(in *chain.Input) (*pb.Input) {
	if in == nil {
		return nil
	}
	return &pb.Input{
		BlockNum:     SerializeBig(util.Uint642Big(in.BlkNum)),
		TxIdx:        SerializeBig(util.Uint322Big(in.TxIdx)),
		OutIdx:       SerializeBig(util.Uint82Big(in.OutIdx)),
		Owner:        in.Owner.Bytes(),
		DepositNonce: SerializeBig(in.DepositNonce),
	}
}

func DeserializeInput(in *pb.Input) (*chain.Input) {
	if in == nil {
		return chain.ZeroInput()
	}
	return &chain.Input{
		DepositNonce: DeserializeBig(in.DepositNonce),
		Owner:        common.BytesToAddress(in.Owner),
		BlkNum:       util.Big2Uint64(DeserializeBig(in.BlockNum)),
		TxIdx:        util.Big2Uint32(DeserializeBig(in.TxIdx)),
		OutIdx:       util.Big2Uint8(DeserializeBig(in.OutIdx)),
	}
}

func SerializeOutput(out *chain.Output) (*pb.Output) {
	if out == nil {
		return nil
	}
	return &pb.Output{
		NewOwner:     out.Owner.Bytes(),
		Amount:       SerializeBig(out.Denom),
		DepositNonce: SerializeBig(out.DepositNonce),
	}
}

func DeserializeOutput(out *pb.Output) (*chain.Output) {
	if out == nil {
		return chain.ZeroOutput()
	}
	return &chain.Output{
		Owner:        common.BytesToAddress(out.NewOwner),
		Denom:        DeserializeBig(out.Amount),
		DepositNonce: DeserializeBig(out.DepositNonce),
	}
}
