/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ethserver

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/rpc"
	"strconv"
	"strings"

	"github.com/gogo/protobuf/proto"
	"github.com/gorilla/mux"
	"github.com/hyperledger/fabric-sdk-go/api/apitxn"
	"github.com/hyperledger/fabric-sdk-go/pkg/config"
	"github.com/hyperledger/fabric-sdk-go/pkg/fabsdk"
	"github.com/hyperledger/fabric/protos/common"
	"github.com/hyperledger/fabric/protos/peer"
	"github.com/rs/cors"
)

type EthRPCService struct {
	EthService
}

type EthService interface {
	GetCode(*http.Request, *DataParam, *string) error
	Call(*http.Request, *Params, *string) error
	SendTransaction(*http.Request, *Params, *string) error
	GetTransactionReceipt(*http.Request, *DataParam, *TxReceipt) error
	Accounts(*http.Request, *DataParam, *[]string) error
}

type ethRPCService struct {
	sdk  *fabsdk.FabricSDK
	user string
}

type DataParam string
type Params struct {
	From     string
	To       string
	Gas      string
	GasPrice string
	Value    string
	Data     string
	Nonce    string
}

type TxReceipt struct {
	TransactionHash   string
	BlockHash         string
	BlockNumber       string
	ContractAddress   string
	GasUsed           int
	CumulativeGasUsed int
}

type EthServer struct {
	Server   *rpc.Server
	listener net.Listener
}

var channelID = "channel1"
var zeroAddress = make([]byte, 20)

func NewEthService(configFile, user string) EthService {
	fmt.Println(configFile)
	c := config.FromFile(configFile)
	sdk, err := fabsdk.New(c)
	if err != nil {
		log.Panic("error creating sdk: ", err)
	}

	return &ethRPCService{
		sdk:  sdk,
		user: user,
	}
}

func NewEthServer(eth EthService) *EthServer {
	server := rpc.NewServer()

	ethService := EthRPCService{eth}
	server.RegisterCodec(NewRPCCodec(), "application/json")
	server.RegisterService(ethService, "eth")

	return &EthServer{
		Server: server,
	}
}

func (s *EthServer) Start(port int) {
	r := mux.NewRouter()
	r.Handle("/", s.Server)

	fmt.Println("Starting the server on port: %d", port)
        // TODO: this could use some work to limit domain to localhost
	handler := cors.Default().Handler(r)
	http.ListenAndServe(fmt.Sprintf(":%d", port), handler)
}

func (req *ethRPCService) GetCode(r *http.Request, args *DataParam, reply *string) error {
	fmt.Println("Recieved a request for GetCode")

	chClient, err := req.sdk.NewChannelClient(channelID, req.user)
	if err != nil {
		log.Panic("error creating client", err)
	}

	defer chClient.Close()

	queryArgs := [][]byte{[]byte(Strip0xFromHex(string(*args)))}

	fmt.Println("About to query the `evmscc`")
	value, err := Query(chClient, "evmscc", "getCode", queryArgs)
	if err != nil {
		fmt.Printf("Failed to query: %s\n", err)
	}
	fmt.Println("About to query the `evmscc`")
	*reply = string(value)

	fmt.Println("Returning from GetCode")

	return nil
}

func (req *ethRPCService) Call(r *http.Request, params *Params, reply *string) error {

	fmt.Println("Received a request for Call")
	fmt.Printf("Data that is being sent:%s \n\n", params.Data)

	chClient, err := req.sdk.NewChannelClient(channelID, req.user)
	if err != nil {
		return err
	}
	defer chClient.Close()

	args := [][]byte{[]byte(Strip0xFromHex(params.Data))}

	fmt.Println("About to query the `evmscc`")
	value, err := Query(chClient, "evmscc", Strip0xFromHex(params.To), args)
	if err != nil {
		fmt.Printf("Failed to query: %s\n", err)
		return err
	}

	*reply = "0x" + hex.EncodeToString(value)
	fmt.Println("Returning from Call")

	return nil
}

func (req *ethRPCService) SendTransaction(r *http.Request, params *Params, reply *string) error {
	fmt.Println("Recieved a request for SendTransaction")
	fmt.Printf("Data that is being sent:%s \n\n", params.Data)
	chClient, err := req.sdk.NewChannelClient(channelID, req.user)
	if err != nil {
		return err
	}
	defer chClient.Close()

	if params.To == "" {
		params.To = hex.EncodeToString(zeroAddress)
	}

	txReq := apitxn.ExecuteTxRequest{
		ChaincodeID: "evmscc",
		Fcn:         Strip0xFromHex(params.To),
		Args:        [][]byte{[]byte(Strip0xFromHex(params.Data))},
	}

	fmt.Println("About to execute a transaction")
	//Return only the transaction ID
	//Maybe change to an async transaction
	_, txID, err := chClient.ExecuteTx(txReq)
	if err != nil {
		fmt.Printf("Failed to execute transaction: %s\n", err)
		return err
	}

	*reply = txID.ID
	fmt.Println("Returning from SendTransaction, returning txID: ", txID.ID)

	return nil
}

func (req *ethRPCService) GetTransactionReceipt(r *http.Request, param *DataParam, reply *TxReceipt) error {
	fmt.Println("Recieved a request for GetTransactionReceipt")

	chClient, err := req.sdk.NewChannelClient(channelID, req.user)

	args := [][]byte{[]byte(channelID), []byte(*param)}

	t, err := Query(chClient, "qscc", "GetTransactionByID", args)
	if err != nil {
		return err
	}

	tx := &peer.ProcessedTransaction{}
	err = proto.Unmarshal(t, tx)
	if err != nil {
		return err
	}

	b, err := Query(chClient, "qscc", "GetBlockByTxID", args)
	if err != nil {
		fmt.Printf("Failed to query qscc: %s\n", err)
		return err
	}

	block := &common.Block{}
	err = proto.Unmarshal(b, block)
	if err != nil {
		return err
	}

	blkHeader := block.GetHeader()

	p := tx.GetTransactionEnvelope().GetPayload()
	payload := &common.Payload{}
	err = proto.Unmarshal(p, payload)
	if err != nil {
		return err
	}

	txActions := &peer.Transaction{}
	err = proto.Unmarshal(payload.GetData(), txActions)

	if err != nil {
		return err
	}

	actions := txActions.GetActions()

	ccPropPayload, respPayload, err := GetPayloads(actions[0])
	if err != nil {
		return err
	}

	invokeSpec := &peer.ChaincodeInvocationSpec{}
	err = proto.Unmarshal(ccPropPayload.Input, invokeSpec)
	if err != nil {
		return err
	}

	receipt := TxReceipt{
		TransactionHash:   string(*param),
		BlockHash:         hex.EncodeToString(blkHeader.Hash()),
		BlockNumber:       strconv.FormatUint(blkHeader.GetNumber(), 10),
		GasUsed:           0,
		CumulativeGasUsed: 0,
	}

	args = invokeSpec.GetChaincodeSpec().GetInput().Args
	// First arg is the callee address. If it is zero address, tx was a contract creation
	callee, err := hex.DecodeString(string(args[0]))
	if err != nil {
		return err
	}

	if bytes.Equal(callee, zeroAddress) {
		receipt.ContractAddress = string(respPayload.GetResponse().GetPayload())
	}
	*reply = receipt

	fmt.Println("Returning from GetTransactionReceipt, returing receipt: ", receipt)

	return nil
}

func (req *ethRPCService) Accounts(r *http.Request, params *DataParam, reply *[]string) error {
	fmt.Println("Recieved a request for Accounts")

	chClient, err := req.sdk.NewChannelClient(channelID, req.user)
	if err != nil {
		log.Panic("error creating client", err)
		return err
	}

	defer chClient.Close()

	queryArgs := [][]byte{}

	fmt.Println("About to query the `evmscc`")
	value, err := Query(chClient, "evmscc", "account", queryArgs)
	if err != nil {
		fmt.Printf("Failed to query: %s\n", err)
		return err
	}
	fmt.Println("About to query the `evmscc`")
	*reply = []string{"0x" + strings.ToLower(string(value))}

	fmt.Println("Returning from Accounts")

	return nil
}

func Query(chClient apitxn.ChannelClient, chaincodeID string, function string, queryArgs [][]byte) ([]byte, error) {

	return chClient.Query(apitxn.QueryRequest{
		ChaincodeID: chaincodeID,
		Fcn:         function,
		Args:        queryArgs,
	})
}

func Strip0xFromHex(addr string) string {
	stripped := strings.Split(addr, "0x")
	// if len(stripped) != 1 {
	// 	panic("Had more then 1 0x in address")
	// }
	return stripped[len(stripped)-1]
}

func GetPayloads(txActions *peer.TransactionAction) (*peer.ChaincodeProposalPayload, *peer.ChaincodeAction, error) {
	// TODO: pass in the tx type (in what follows we're assuming the type is ENDORSER_TRANSACTION)
	ccPayload := &peer.ChaincodeActionPayload{}
	err := proto.Unmarshal(txActions.Payload, ccPayload)
	if err != nil {
		return nil, nil, err
	}

	if ccPayload.Action == nil || ccPayload.Action.ProposalResponsePayload == nil {
		return nil, nil, fmt.Errorf("no payload in ChaincodeActionPayload")
	}

	ccProposalPayload := &peer.ChaincodeProposalPayload{}
	err = proto.Unmarshal(ccPayload.ChaincodeProposalPayload, ccProposalPayload)
	if err != nil {
		return nil, nil, err
	}

	pRespPayload := &peer.ProposalResponsePayload{}
	err = proto.Unmarshal(ccPayload.Action.ProposalResponsePayload, pRespPayload)
	if err != nil {
		return nil, nil, err
	}

	if pRespPayload.Extension == nil {
		return nil, nil, fmt.Errorf("response payload is missing extension")
	}

	respPayload := &peer.ChaincodeAction{}
	err = proto.Unmarshal(pRespPayload.Extension, respPayload)
	if err != nil {
		return ccProposalPayload, nil, err
	}
	return ccProposalPayload, respPayload, nil
}
