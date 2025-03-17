package main

import (
	"encoding/hex"
	"log"
	"os"
	"taponark"

	"gopkg.in/yaml.v2"
)

type app struct {
	serverTapClient         taponark.TapClient
	boardingUserTapClient   taponark.TapClient
	exitUserTapClient       taponark.TapClient
	bitcoinClient           taponark.BitcoinClient
	boardingTransferDetails *taponark.ArkBoardingTransfer
	vtxoList                []taponark.VirtualTxOut
	roundRootProofFile      []byte
}

func Init() app {
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

	boardingUserLndClient := taponark.InitLndClient(config.OnboardingUserLndClient)
	boardingUserTapClient := taponark.InitTapClient(config.OnboardingUserTapClient, boardingUserLndClient)

	exitUserLndClient := taponark.InitLndClient(config.ExitUserLndClient)
	exitUserTapClient := taponark.InitTapClient(config.ExitUserTapClient, exitUserLndClient)

	serverLndClient := taponark.InitLndClient(config.ServerLndClient)
	serverTapClient := taponark.InitTapClient(config.ServerTapClient, serverLndClient)

	bitcoinClient := taponark.GetBitcoinClient(config.BitcoinClient)

	log.Println("All clients Initilised")
	return app{serverTapClient, boardingUserTapClient, exitUserTapClient, bitcoinClient, nil, []taponark.VirtualTxOut{}, nil}
}

func (ap *app) Board() {
	assetId, _ := hex.DecodeString("43902e99a18ff431608ff47d871e3367d9a729c6d3e13358bbb653fc97f1df16")
	boardingAssetAmnt := 40
	boardingBtcAmnt := 100_000

	// Onboard Asset and Btc
	boardingTransferDetails := taponark.OnboardUser(assetId, uint64(boardingAssetAmnt), uint64(boardingBtcAmnt), &ap.boardingUserTapClient, &ap.serverTapClient, &ap.bitcoinClient)
	ap.boardingTransferDetails = &boardingTransferDetails

	log.Println("Boarding User Complete")
}

func (ap *app) ConstructRound() {
	roundTreeLevel := uint64(2)
	assetId, _ := hex.DecodeString("43902e99a18ff431608ff47d871e3367d9a729c6d3e13358bbb653fc97f1df16")

	vtxoList, roundRootProofFile := taponark.CreateRoundTransfer(*ap.boardingTransferDetails, assetId, &ap.exitUserTapClient, &ap.serverTapClient, roundTreeLevel, ap.bitcoinClient)

	ap.vtxoList = vtxoList
	ap.roundRootProofFile = roundRootProofFile
}

func (ap *app) UploadProofs() {
	assetId, _ := hex.DecodeString("43902e99a18ff431608ff47d871e3367d9a729c6d3e13358bbb653fc97f1df16")
	taponark.PublishTransfersAndSubmitProofs(assetId, proofList, fullproof.GenesisPoint, proofFile, &ap.exitUserTapClient, bitcoinClient)
}
