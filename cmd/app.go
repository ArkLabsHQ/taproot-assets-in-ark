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
	round                   taponark.Round
	roundRootProofFile      []byte
	assetId                 []byte
	assetVtxoProofList      [][]byte
}

func DeriveLndTlsAndMacaroonHex(container string, network string) (string, string) {
	tlsPath := filepath.Join("data", network, "volumes", "lnd", container, "tls.cert")
	tlsBytes, err := os.ReadFile(tlsPath)
	if err != nil {
		log.Fatalf("failed to read TLS certificate: %v", err)
	}
	tlsHex := hex.EncodeToString(tlsBytes)

	// Read macaroon file
	macaroonFileDir := network
	if network == "mutinynet" {
		macaroonFileDir = "signet"
	}
	macaroonPath := filepath.Join("data", network, "volumes", "lnd", container, "data", "chain", "bitcoin", macaroonFileDir, "admin.macaroon")
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
	macaroonFileDir := network
	if network == "mutinynet" {
		macaroonFileDir = "signet"
	}
	macaroonPath := filepath.Join("data", network, "volumes", "tapd", container, "data", macaroonFileDir, "admin.macaroon")
	macaroonBytes, err := os.ReadFile(macaroonPath)
	if err != nil {
		log.Fatalf("failed to read macaroon file: %v", err)
	}
	macaroonHex := hex.EncodeToString(macaroonBytes)
	return tlsHex, macaroonHex
}

func InitConfig(network string) taponark.Config {
	// Read the YAML configuration file
	configFile := "config-regtest.yaml"

	if network == "signet" {
		configFile = "config-signet.yaml"
	} else if network == "mutinynet" {
		configFile = "config-mutinynet.yaml"
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
	} else if network == "mutinynet" {
		if config.SignetChallenge == nil {
			log.Panicln("Signet Challenge Must be Present in Signet")
		}

		signetChallenge, err := hex.DecodeString(*config.SignetChallenge)
		if err != nil {
			log.Panicf("Signet Challenge cannot be decoded %v", err)
		}

		chainParams = chaincfg.CustomSignetParams(signetChallenge, []chaincfg.DNSSeed{})
		tapParams = address.SigNetTap
	}

	timeout := time.Duration(config.Timeout) * time.Minute
	syncHostname := config.OnboardingUserTapClient.Hostname

	// Init BoardingUser
	boardingUserTapTlsHex, boardingUserTapMacaroonHex := DeriveTapTlsAndMacaroonHex(config.OnboardingUserTapClient.Container, network)
	boardingUserLndTlsHex, boardingUserLndMacaroonHex := DeriveLndTlsAndMacaroonHex(config.OnboardingUserLndClient.Container, network)
	boardingUserLndClient := taponark.InitLndClient(config.OnboardingUserLndClient, boardingUserLndTlsHex, boardingUserLndMacaroonHex)
	boardingUserTapClient := taponark.InitTapClient(syncHostname, config.OnboardingUserTapClient, boardingUserLndClient, boardingUserTapTlsHex, boardingUserTapMacaroonHex, chainParams, tapParams, timeout)

	// Init ExitUser
	exitUserTapTlsHex, exitUserTapMacaroonHex := DeriveTapTlsAndMacaroonHex(config.ExitUserTapClient.Container, network)
	exitUserLndTlsHex, exitUserLndMacaroonHex := DeriveLndTlsAndMacaroonHex(config.ExitUserLndClient.Container, network)
	exitUserLndClient := taponark.InitLndClient(config.ExitUserLndClient, exitUserLndTlsHex, exitUserLndMacaroonHex)
	exitUserTapClient := taponark.InitTapClient(syncHostname, config.ExitUserTapClient, exitUserLndClient, exitUserTapTlsHex, exitUserTapMacaroonHex, chainParams, tapParams, timeout)

	// Init Server
	serverTapTlsHex, serverTapMacaroonHex := DeriveTapTlsAndMacaroonHex(config.ServerTapClient.Container, network)
	serverLndTlsHex, serverLndMacaroonHex := DeriveLndTlsAndMacaroonHex(config.ServerLndClient.Container, network)
	serverLndClient := taponark.InitLndClient(config.ServerLndClient, serverLndTlsHex, serverLndMacaroonHex)
	serverTapClient := taponark.InitTapClient(syncHostname, config.ServerTapClient, serverLndClient, serverTapTlsHex, serverTapMacaroonHex, chainParams, tapParams, timeout)

	bitcoinClient := taponark.GetBitcoinClient(config.BitcoinClient, chainParams, timeout)

	log.Println("All clients Initilised")
	return App{serverTapClient, boardingUserTapClient, exitUserTapClient, bitcoinClient, nil, taponark.Round{}, nil, nil, nil}
}

// Onboarder Mint
func (ap *App) Mint() {
	assetId, err := ap.boardingUserTapClient.CreateAsset()
	if err != nil {
		log.Printf("Error creating asset: %v", err)
		log.Println("-------------------------------------")
		return
	}
	err = ap.serverTapClient.Sync()
	if err != nil {
		log.Printf("Error Sycing Server: %v", err)
		log.Println("-------------------------------------")
		return
	}

	err = ap.exitUserTapClient.Sync()
	if err != nil {
		log.Printf("Error Sycing Exit User: %v", err)
		log.Println("-------------------------------------")
		return
	}

	ap.assetId = assetId
	hexEncodedString := hex.EncodeToString(assetId)
	log.Printf("\nAsset ID: %s", hexEncodedString)
	log.Println("Minting Complete")
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
	log.Println("Boarding User Complete")
	log.Println("------------------------------------------------")

}

func (ap *App) ConstructRound() {
	round, err := taponark.ConstructAndBroadcastRound(ap.assetId, *ap.boardingTransferDetails, &ap.exitUserTapClient, &ap.serverTapClient, ap.bitcoinClient)
	if err != nil {
		log.Printf("Error creating round transfer: %v", err)
		log.Println("-------------------------------------")
		return
	}

	ap.round = round
	log.Println("Round Construction Complete")
	log.Println("------------------------------------------------")
}

func (ap *App) ShowRoundTree() {

	taponark.PrintTree(ap.round.RoundTree.Root, "", true)

	log.Println("Print Of Round Complete")
	log.Println("------------------------------------------------")
}

func (ap *App) ExitRound() {
	assetVtxoProofList, err := taponark.ExitRoundAndAppendProof(ap.round, &ap.bitcoinClient)
	if err != nil {
		log.Printf("Error Exitng round or appending round: %v", err)
		log.Println("-------------------------------------")
		return
	}
	ap.assetVtxoProofList = assetVtxoProofList
	log.Println("Exit Transactions Broadcasted and Token Transfer Proof Appended")
	log.Println("------------------------------------------------")

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

func (ap *App) UploadTokenVtxoProof() {
	length := len(ap.assetVtxoProofList)
	if length == 0 {
		log.Println("No Vtxo Proofs to Upload")
		log.Println("-------------------------------------")
		return
	}

	proofFile := ap.assetVtxoProofList[0]
	err := taponark.SubmitProof(ap.round.GenesisPoint, proofFile, &ap.exitUserTapClient)
	if err != nil {
		log.Printf("Error uploading proof: %v", err)
		log.Println("-------------------------------------")
		return
	}

	ap.assetVtxoProofList = ap.assetVtxoProofList[1:]
	log.Println("Proof Uploaded")
	log.Println("------------------------------------------------")
}
