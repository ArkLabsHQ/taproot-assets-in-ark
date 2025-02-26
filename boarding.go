package taponark

import (
	"log"

	"github.com/btcsuite/btcd/txscript"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/asset"
)

// user tap client

func SpendToBoardingTransaction(assetId []byte, amnt uint64, lockHeight uint32, userTapClient, serverTapClient *TapClient) ArkTransferOutputDetails {
	userScriptKey, userInternalKey := userTapClient.GetNextKeys()
	serverScriptKey, serverInternalKey := serverTapClient.GetNextKeys()

	// 1. Create Both Bording Ark Script and Boarding Ark Asset Script
	arkScript, err := CreateBoardingArkScript(userInternalKey.PubKey, serverInternalKey.PubKey, lockHeight)
	if err != nil {
		log.Fatal(err)
	}
	arkAssetScript := CreateBoardingArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey, lockHeight)
	if err != nil {
		log.Fatal(err)
	}

	addr, err := serverTapClient.GetBoardingAddress(arkScript.Branch, arkAssetScript.tapScriptKey, assetId, amnt)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Send From User To Boarding Address
	sendResp, err := userTapClient.SendAsset(addr)

	if err != nil {
		log.Fatalf("cannot send to address %v", err)
	}

	// spend from Boarding Address
	btcInternalKey := asset.NUMSPubKey
	btcControlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: btcInternalKey,
	}
	// Transfer is always output 1
	transferOutput := sendResp.Transfer.Outputs[1]
	multiSigOutAnchor := transferOutput.Anchor
	rightNodeHash := arkScript.unilateralSpend.TapHash()
	inclusionproof := append(rightNodeHash[:], multiSigOutAnchor.TaprootAssetRoot[:]...)
	btcControlBlock.InclusionProof = inclusionproof
	rootHash := btcControlBlock.RootHash(arkScript.cooperativeSpend.Script)
	tapKey := txscript.ComputeTaprootOutputKey(btcInternalKey, rootHash)
	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		btcControlBlock.OutputKeyYIsOdd = true
	}

	//TODO (Joshua) Ensure To Improve
	log.Println("Asset Transfered Please Mine")
	bcoinClient := GetBitcoinClient()
	address1, err := bcoinClient.GetNewAddress("")
	if err != nil {
		log.Fatalf("cannot generate address %v", err)
	}
	maxretries := int64(3)
	_, err = bcoinClient.GenerateToAddress(1, address1, &maxretries)
	if err != nil {
		log.Fatalf("cannot generate to address %v", err)
	}
	serverTapClient.IncomingTransferEvent(addr)

	return ArkTransferOutputDetails{btcControlBlock, userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript, transferOutput, addr}
}
