package main

import (
	"encoding/hex"
	"log"
	"os"
	"taponark"

	"gopkg.in/yaml.v2"
)

func main() {

	// Read the YAML configuration file
	data, err := os.ReadFile("config.yaml")
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	// Create an instance of the Config struct
	var config taponark.Config

	// Unmarshal the YAML data into the struct
	err = yaml.Unmarshal(data, &config)
	if err != nil {
		log.Fatalf("error: %v", err)
	}

	assetId, _ := hex.DecodeString("43902e99a18ff431608ff47d871e3367d9a729c6d3e13358bbb653fc97f1df16")
	boardingAssetAmnt := 40
	boardingBtcAmnt := 100_000

	boardingUserLndClient := taponark.InitLndClient(config.OnboardingUserLndClient)
	boardingUserTapClient := taponark.InitTapClient(config.OnboardingUserTapClient, boardingUserLndClient)

	exitUserLndClient := taponark.InitLndClient(config.ExitUserLndClient)
	exitUserTapClient := taponark.InitTapClient(config.ExitUserTapClient, exitUserLndClient)

	serverLndClient := taponark.InitLndClient(config.ServerLndClient)
	serverTapClient := taponark.InitTapClient(config.ServerTapClient, serverLndClient)

	bitcoinClient := taponark.GetBitcoinClient(config.BitcoinClient)

	boardingTransfer := taponark.SpendToBoardingTransaction(assetId, uint64(boardingAssetAmnt), uint64(boardingBtcAmnt), &boardingUserTapClient, &serverTapClient, bitcoinClient)

	roundRootSpendingDetails := taponark.CreateRoundSpendingDetails(&exitUserTapClient, &serverTapClient)
	roundRootTransfer := taponark.SpendFromBoardingTransfer(assetId, boardingTransfer, roundRootSpendingDetails, &serverTapClient)

	fullproof := serverTapClient.ExportProof(assetId,
		boardingTransfer.AssetTransferDetails.AssetTransferOutput.ScriptKey,
	)

	proofList, proofFile := taponark.CreateRoundTransfer(fullproof.RawProofFile, roundRootSpendingDetails, roundRootTransfer, assetId, &exitUserTapClient, &serverTapClient, 2, bitcoinClient)
	taponark.PublishTransfersAndSubmitProofs(assetId, proofList, fullproof.GenesisPoint, proofFile, &exitUserTapClient, bitcoinClient)
	// wait for transfer to reach

	log.Println("done with submission of proofs")

}
