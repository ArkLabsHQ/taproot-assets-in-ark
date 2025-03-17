package main

import (
	"encoding/hex"
	"log"
	"os"
	"taponark"

	"gopkg.in/yaml.v2"
)

func main() {

	

	

	

	

	proofList, proofFile := taponark.CreateRoundTransfer(fullproof.RawProofFile, roundRootSpendingDetails, roundRootTransfer, assetId, &exitUserTapClient, &serverTapClient, 2, bitcoinClient)
	taponark.PublishTransfersAndSubmitProofs(assetId, proofList, fullproof.GenesisPoint, proofFile, &exitUserTapClient, bitcoinClient)
	// wait for transfer to reach

	log.Println("done with submission of proofs")

}

InitializeClient()

