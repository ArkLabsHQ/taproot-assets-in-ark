package main

import (
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"taponark"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightninglabs/taproot-assets/address"
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
	assetId                 []byte
}

func DeriveLndTlsAndMacaroonHex(container string, network string) (string, string) {
	tlsPath := filepath.Join("data", network, "volumes", "lnd", container, "tls.cert")
	tlsBytes, err := os.ReadFile(tlsPath)
	if err != nil {
		log.Fatalf("failed to read TLS certificate: %v", err)
	}
	tlsHex := hex.EncodeToString(tlsBytes)

	// Read macaroon file
	macaroonPath := filepath.Join("data", network, "volumes", "lnd", container, "data", "chain", "bitcoin", network, "admin.macaroon")
	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		log.Fatalf("failed to read macaroon file: %v", err)
	}
	macaroonHex := hex.EncodeToString(macaroonBytes)
	return tlsHex, macaroonHex
}

func DeriveTapTlsAndMacaroonHex(container string, network string) (string, string) {
	tlsPath := filepath.Join("data", network, "volumes", "tapd", container, "tls.cert")
	tlsBytes, err := os.ReadFile(tlsPath)
	if err != nil {
		log.Fatalf("failed to read TLS certificate: %v", err)
	}
	tlsHex := hex.EncodeToString(tlsBytes)

	// Read macaroon file
	macaroonPath := filepath.Join("data", network, "volumes", "tapd", container, "data", network, "admin.macaroon")
	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		log.Fatalf("failed to read macaroon file: %v", err)
	}
	macaroonHex := hex.EncodeToString(macaroonBytes)
	return tlsHex, macaroonHex
}

func InitConfig(network string) taponark.Config {
	// Read the YAML configuration file
	configFile := "config.yaml"

	if network == "signet" {
		configFile = "config-signet.yaml"
	}
	data, err := os.ReadFile(configFile)
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

	return config
}

func Init(network string) App {
	// Read the YAML configuration file
	config := InitConfig(network)

	chainParams := chaincfg.RegressionNetParams
	tapParams := address.RegressionNetTap
	if network == "signet" {
		chainParams = chaincfg.SigNetParams
		tapParams = address.SigNetTap
	}

	timeout := time.Duration(config.Timeout) * time.Minute

	// Init BoardingUser
	boardingUserTapTlsHex, boardingUserTapMacaroonHex := DeriveTapTlsAndMacaroonHex(config.OnboardingUserTapClient.Container, network)
	boardingUserLndTlsHex, boardingUserLndMacaroonHex := DeriveLndTlsAndMacaroonHex(config.OnboardingUserLndClient.Container, network)
	boardingUserLndClient := taponark.InitLndClient(config.OnboardingUserLndClient, boardingUserLndTlsHex, boardingUserLndMacaroonHex)
	boardingUserTapClient := taponark.InitTapClient(config.OnboardingUserTapClient, boardingUserLndClient, boardingUserTapTlsHex, boardingUserTapMacaroonHex, chainParams, tapParams, timeout)

	// Init ExitUser
	exitUserTapTlsHex, exitUserTapMacaroonHex := DeriveTapTlsAndMacaroonHex(config.ExitUserTapClient.Container, network)
	exitUserLndTlsHex, exitUserLndMacaroonHex := DeriveLndTlsAndMacaroonHex(config.ExitUserLndClient.Container, network)
	exitUserLndClient := taponark.InitLndClient(config.ExitUserLndClient, exitUserLndTlsHex, exitUserLndMacaroonHex)
	exitUserTapClient := taponark.InitTapClient(config.ExitUserTapClient, exitUserLndClient, exitUserTapTlsHex, exitUserTapMacaroonHex, chainParams, tapParams, timeout)

	// Init Server
	serverTapTlsHex, serverTapMacaroonHex := DeriveTapTlsAndMacaroonHex(config.ServerTapClient.Container, network)
	serverLndTlsHex, serverLndMacaroonHex := DeriveLndTlsAndMacaroonHex(config.ServerLndClient.Container, network)
	serverLndClient := taponark.InitLndClient(config.ServerLndClient, serverLndTlsHex, serverLndMacaroonHex)
	serverTapClient := taponark.InitTapClient(config.ServerTapClient, serverLndClient, serverTapTlsHex, serverTapMacaroonHex, chainParams, tapParams, timeout)

	bitcoinClient := taponark.GetBitcoinClient(config.BitcoinClient, chainParams, timeout)

	log.Println("All clients Initilised")
	return App{serverTapClient, boardingUserTapClient, exitUserTapClient, bitcoinClient, nil, []taponark.VirtualTxOut{}, nil, nil}
}

// Onboarder Mint
func (ap *App) Mint() {
	assetId, err := ap.boardingUserTapClient.CreateAsset()
	if err != nil {
		log.Printf("Error creating asset: %v", err)
		log.Println("-------------------------------------")
		return
	}
	err = ap.serverTapClient.Sync("onboarduser-tap")
	if err != nil {
		log.Printf("Error Sycing Server: %v", err)
		log.Println("-------------------------------------")
		return
	}

	err = ap.exitUserTapClient.Sync("onboarduser-tap")
	if err != nil {
		log.Printf("Error Sycing Exit User: %v", err)
		log.Println("-------------------------------------")
		return
	}

	ap.assetId = assetId
	hexEncodedString := hex.EncodeToString(assetId)
	log.Printf("\nAsset ID: %s", hexEncodedString)
	log.Println("-------------------------------------")
}

func (ap *App) FundOnboarding() {
	// Fund the onboarding user
	boardingUserAddr, err := ap.boardingUserTapClient.GetBtcAddress()
	if err != nil {
		log.Printf("Error getting deposit address: %v", err)
		log.Println("-------------------------------------")
		return
	}

	log.Printf("\nDeposit Address For Onboarding User : %s", boardingUserAddr)
	log.Println("-------------------------------------")
}

func (ap *App) Board() {
	boardingAssetAmnt := 40
	boardingBtcAmnt := 100_000

	// Onboard Asset and Btc
	boardingTransferDetails, err := taponark.OnboardUser(ap.assetId, uint64(boardingAssetAmnt), uint64(boardingBtcAmnt), &ap.boardingUserTapClient, &ap.serverTapClient, &ap.bitcoinClient)
	if err != nil {
		log.Printf("Error onboarding user: %v", err)
		log.Println("-------------------------------------")
		return
	}
	ap.boardingTransferDetails = &boardingTransferDetails

}

func (ap *App) ConstructRound() {
	roundTreeLevel := uint64(2)

	vtxoList, roundRootProofFile, err := taponark.CreateRoundTransfer(*ap.boardingTransferDetails, ap.assetId, &ap.exitUserTapClient, &ap.serverTapClient, roundTreeLevel, ap.bitcoinClient)
	if err != nil {
		log.Printf("Error creating round transfer: %v", err)
		log.Println("-------------------------------------")
		return
	}

	ap.vtxoList = vtxoList
	ap.roundRootProofFile = roundRootProofFile
}

func (ap *App) UploadProofs() {
	err := taponark.PublishTransfersAndSubmitProofs(ap.assetId, ap.vtxoList, ap.boardingTransferDetails.AssetTransferDetails.GenesisPoint, ap.roundRootProofFile, &ap.exitUserTapClient, &ap.bitcoinClient)
	if err != nil {
		log.Printf("Error uploading proofs: %v", err)
		log.Println("-------------------------------------")
		return
	}
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

	boardingUserAssetBalance, boardingUserBtcBalance, err := ap.boardingUserTapClient.GetBalance(ap.assetId)
	if err != nil {
		log.Printf("Error getting balance: %v", err)
		log.Println("-------------------------------------")
		return
	}

	log.Println("------Boarding User-----")
	log.Printf("Asset Balance = %d", boardingUserAssetBalance)
	log.Printf("Btc Balance = %d", boardingUserBtcBalance)
	log.Println("-------------------------------------")

	exitUserAssetBalance, exitUserBtcBalance, err := ap.exitUserTapClient.GetBalance(ap.assetId)
	if err != nil {
		log.Printf("Error getting balance: %v", err)
		log.Println("-------------------------------------")
		return
	}

	log.Println("-----Exit User------")
	log.Printf("Asset Balance = %d", exitUserAssetBalance)
	log.Printf("Btc Balance = %d", exitUserBtcBalance)
	log.Println("-------------------------------------")
}
