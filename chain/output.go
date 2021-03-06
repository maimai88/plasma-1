package chain

import (
		"github.com/ethereum/go-ethereum/common"
		"math/big"
	)

type Output struct {
	Owner        common.Address
	Denom        *big.Int
	DepositNonce *big.Int
}

func NewOutput(newOwner common.Address, amount, depositNonce *big.Int) *Output {
	return &Output{
		Owner: common.BytesToAddress(newOwner.Bytes()),
		Denom: big.NewInt(amount.Int64()),
		DepositNonce: big.NewInt(depositNonce.Int64()),
	}
}

func ZeroOutput() *Output {
	return &Output{
		Owner:        common.BytesToAddress(make([]byte, 20, 20)),
		Denom:        big.NewInt(0),
		DepositNonce: big.NewInt(0),
	}
}

func (out *Output) IsExit() bool {
	if out == nil {
		return false
	}
	exit := ExitOutput()
	for i := 0; i != len(out.Owner); i++ {
		if out.Owner[i] != exit.Owner[i] {
			return false
		}
	}
	return true
}

func (out *Output) IsDeposit() bool {
	if out == nil {
		return false
	}
	return out.DepositNonce != nil && out.DepositNonce.Cmp(Zero()) != 0
}

func (out *Output) IsZeroOutput() bool {
	if out == nil {
		return true
	}
	addrBytes := out.Owner.Bytes()

	for _, v := range addrBytes {
		if v != 0 {
			return false
		}
	}

	return (out.Denom == nil ||out.Denom.Cmp(Zero()) == 0) &&
		(out.DepositNonce == nil || out.DepositNonce.Cmp(Zero()) == 0)
}