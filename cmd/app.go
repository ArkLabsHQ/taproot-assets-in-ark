package main

import (
	"encoding/hex"
	"log"
	"os"
	"taponark"

	"gopkg.in/yaml.v2"
)

type App struct {
	serverTapClient         taponark.TapClient
	boardingUserTapClient   taponark.TapClient
	exitUserTapClient       taponark.TapClient
	bitcoinClient           taponark.BitcoinClient
	boardingTransferDetails *taponark.ArkBoardingTransfer
	vtxoList                []taponark.VirtualTxOut
	roundRootProofFile      []byte
}

func Init() App {
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
	return App{serverTapClient, boardingUserTapClient, exitUserTapClient, bitcoinClient, nil, []taponark.VirtualTxOut{}, nil}
}

func (ap *App) Board() {
	assetId, _ := hex.DecodeString("43902e99a18ff431608ff47d871e3367d9a729c6d3e13358bbb653fc97f1df16")
	boardingAssetAmnt := 40
	boardingBtcAmnt := 100_000

	// Onboard Asset and Btc
	boardingTransferDetails := taponark.OnboardUser(assetId, uint64(boardingAssetAmnt), uint64(boardingBtcAmnt), &ap.boardingUserTapClient, &ap.serverTapClient, &ap.bitcoinClient)
	ap.boardingTransferDetails = &boardingTransferDetails

}

func (ap *App) ConstructRound() {
	roundTreeLevel := uint64(2)
	assetId, _ := hex.DecodeString("43902e99a18ff431608ff47d871e3367d9a729c6d3e13358bbb653fc97f1df16")

	vtxoList, roundRootProofFile := taponark.CreateRoundTransfer(*ap.boardingTransferDetails, assetId, &ap.exitUserTapClient, &ap.serverTapClient, roundTreeLevel, ap.bitcoinClient)

	ap.vtxoList = vtxoList
	ap.roundRootProofFile = roundRootProofFile
}

func (ap *App) UploadProofs() {
	assetId, _ := hex.DecodeString("43902e99a18ff431608ff47d871e3367d9a729c6d3e13358bbb653fc97f1df16")
	taponark.PublishTransfersAndSubmitProofs(assetId, ap.vtxoList, ap.boardingTransferDetails.AssetTransferDetails.GenesisPoint, ap.roundRootProofFile, &ap.exitUserTapClient, &ap.bitcoinClient)
}

func (ap *App) ShowVtxos() {
	intermediateLeft := ap.vtxoList[0]
	intermediateRight := ap.vtxoList[1]
	log.Println("------Intermediate Transaction-----")
	log.Printf("Left Branch:  Asset Amount = %d, Btc Amount = %d", intermediateLeft.AssetAmount, intermediateLeft.BtcAmount)
	log.Printf("Right Branch:  Asset Amount = %d, Btc Amount = %d", intermediateRight.AssetAmount, intermediateRight.BtcAmount)
	log.Printf("\nTransaction Hash: %s", intermediateLeft.TxMsg.TxID())
	log.Println("-------------------------------------")

	leftLeafAsset := ap.vtxoList[2]
	leftLeftBtc := ap.vtxoList[3]
	log.Println("------Left Leaf Transaction-----")
	log.Printf("Left Branch:  Asset Amount = %d", leftLeafAsset.AssetAmount)
	log.Printf("Right Branch: Btc Amount = %d", leftLeftBtc.BtcAmount)
	log.Printf("\nTransaction Hash: %s", leftLeafAsset.TxMsg.TxID())
	log.Println("-------------------------------------")

	rightLeafAsset := ap.vtxoList[2]
	rightLeftBtc := ap.vtxoList[3]
	log.Println("------Right Leaf Transaction-----")
	log.Printf("Left Branch:  Asset Amount = %d", rightLeafAsset.AssetAmount)
	log.Printf("Right Branch: Btc Amount = %d", rightLeftBtc.BtcAmount)
	log.Printf("\nTransaction Hash: %s", rightLeftBtc.TxMsg.TxID())
	log.Println("-------------------------------------")
}

func (ap *App) ShowBalance() {
	assetId, _ := hex.DecodeString("43902e99a18ff431608ff47d871e3367d9a729c6d3e13358bbb653fc97f1df16")

	boardingUserAssetBalance, boardingUserBtcBalance := ap.boardingUserTapClient.GetBalance(assetId)
	log.Println("------Boarding User-----")
	log.Printf("Asset Balance = %d", boardingUserAssetBalance)
	log.Printf("Btc Balance = %d", boardingUserBtcBalance)
	log.Println("-------------------------------------")

	exitUserAssetBalance, exitUserBtcBalance := ap.exitUserTapClient.GetBalance(assetId)
	log.Println("-----Exit User------")
	log.Printf("Asset Balance = %d", exitUserAssetBalance)
	log.Printf("Btc Balance = %d", exitUserBtcBalance)
	log.Println("-------------------------------------")
}
