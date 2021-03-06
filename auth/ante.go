package auth

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	ethcrypto "github.com/ethereum/go-ethereum/crypto"
	crypto "github.com/tendermint/go-crypto"
	types "github.com/FourthState/plasma-mvp-sidechain/types"
	utils "github.com/FourthState/plasma-mvp-sidechain/utils"
	"reflect"
)

// NewAnteHandler returns an AnteHandler that checks signatures,
// confirm signatures, and increments the feeAmount
func NewAnteHandler(utxoMapper types.UTXOMapper, txIndex *uint16, feeAmount *uint64) sdk.AnteHandler {
	return func(
		ctx sdk.Context, tx sdk.Tx,
	) (_ sdk.Context, _ sdk.Result, abort bool) {

		// Assert that there are signatures
		sigs := tx.GetSignatures()
		if len(sigs) == 0 {
			return ctx,
				sdk.ErrUnauthorized("no signers").Result(),
				true
		}

		msg := tx.GetMsg()

		_, ok := tx.(types.BaseTx)
		if !ok {
			return ctx, sdk.ErrInternal("tx must be in form of BaseTx").Result(), true
		}

		// Assert that number of signatures is correct.
		var signerAddrs = msg.GetSigners()

		if len(sigs) != len(signerAddrs) {
			return ctx,
				sdk.ErrUnauthorized("wrong number of signers").Result(),
				true
		}

		spendMsg, ok := msg.(types.SpendMsg)
		if !ok {
			return ctx, sdk.ErrInternal("Msg must be of type SpendMsg").Result(), true
		}
		signBytes := spendMsg.GetSignBytes()

		// Verify the first input signature
		position1 := types.Position{spendMsg.Blknum1, spendMsg.Txindex1, spendMsg.Oindex1, spendMsg.DepositNum1}
		res := processSig(ctx, utxoMapper, position1, signerAddrs[0], sigs[0], signBytes)

		if !res.IsOK() {
			return ctx, res, true
		}

		posSignBytes := position1.GetSignBytes()

		// Verify that confirmation signature
		res = processConfirmSig(ctx, utxoMapper, position1, spendMsg.ConfirmSigs1, posSignBytes)
		if !res.IsOK() {
			return ctx, res, true
		}

		// Verify the second input
		if utils.ValidAddress(spendMsg.Owner2) {
			position2 := types.Position{spendMsg.Blknum2, spendMsg.Txindex2, spendMsg.Oindex2, spendMsg.DepositNum2}
			res = processSig(ctx, utxoMapper, position2, signerAddrs[1], sigs[1], signBytes)
			if !res.IsOK() {
				return ctx, res, true
			}

			posSignBytes = position2.GetSignBytes()

			res = processConfirmSig(ctx, utxoMapper, position2, spendMsg.ConfirmSigs2, posSignBytes)
			if !res.IsOK() {
				return ctx, res, true
			}
		}

		// Increment amount of fees collected
		if !ctx.IsCheckTx() {
			(*feeAmount) += spendMsg.Fee
		}

		// TODO: tx tags (?)
		return ctx, sdk.Result{}, false // continue...
	}
}

func processSig(
	ctx sdk.Context, um types.UTXOMapper,
	position types.Position, addr crypto.Address, sig sdk.StdSignature, signBytes []byte) (
	res sdk.Result) {

	// Verify utxo exists
	utxo := um.GetUTXO(ctx, position)
	if utxo == nil {
		return sdk.ErrUnknownRequest("UTXO trying to be spent, does not exist").Result()
	}

	// Verify that utxo owner equals input address in the transaction
	if !reflect.DeepEqual(utxo.GetAddress().Bytes(), addr.Bytes()) {
		return sdk.ErrUnauthorized("signer does not match utxo owner").Result()
	}

	// sig.Signature.Bytes() returns amino encoded signature
	// the first 5 bytes represent the encoding for the signature
	// Bytes 1-4: prefix bytes (crypto.SignatureSecp256k1 has prefix of 0x16E1FEEA)
	// Byte 5: byte array length (should be 0x41 or 65)
	hash := ethcrypto.Keccak256(signBytes)
	pubKey1, err1 := ethcrypto.SigToPub(hash, sig.Signature.Bytes()[5:])

	if err1 != nil || !reflect.DeepEqual(ethcrypto.PubkeyToAddress(*pubKey1).Bytes(), addr.Bytes()) {
		return sdk.ErrUnauthorized("signature verification failed").Result()
	}

	return sdk.Result{}
}

func processConfirmSig(
	ctx sdk.Context, utxoMapper types.UTXOMapper,
	position types.Position, sig [2]crypto.Signature, signBytes []byte) (
	res sdk.Result) {

	// Verify utxo exists
	utxo := utxoMapper.GetUTXO(ctx, position)
	if utxo == nil {
		return sdk.ErrUnknownRequest("UTXO trying to be spent, does not exist").Result()
	}
	inputAddresses := utxo.GetInputAddresses()

	ethsigs := make([]crypto.SignatureSecp256k1, 2)
	for i, s := range sig {
		ethsigs[i] = s.(crypto.SignatureSecp256k1)
	}

	hash := ethcrypto.Keccak256(signBytes)

	// sig.Signature.Bytes() returns amino encoded signature
	// the first 5 bytes represent the encoding for the signature
	// Bytes 1-4: prefix bytes (crypto.SignatureSecp256k1 has prefix of 0x16E1FEEA)
	// Byte 5: byte array length (should be 0x41 or 65)
	pubKey1, err1 := ethcrypto.SigToPub(hash, ethsigs[0].Bytes()[5:])
	if err1 != nil || !reflect.DeepEqual(ethcrypto.PubkeyToAddress(*pubKey1).Bytes(), inputAddresses[0].Bytes()) {
		return sdk.ErrUnauthorized("confirm signature 1 verification failed").Result()
	}

	if utils.ValidAddress(inputAddresses[1]) {
		pubKey2, err2 := ethcrypto.SigToPub(hash, ethsigs[1].Bytes()[5:])
		if err2 != nil || !reflect.DeepEqual(ethcrypto.PubkeyToAddress(*pubKey2).Bytes(), inputAddresses[1].Bytes()) {
			return sdk.ErrUnauthorized("confirm signature 2 verification failed").Result()
		}
	}

	return sdk.Result{}
}
