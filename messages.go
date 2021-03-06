package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"

	zmq "github.com/alecthomas/gozmq"
	uuid "github.com/nu7hatch/gouuid"
	"github.com/pkg/errors"
)

// MsgHeader encodes header info for ZMQ messages.
type MsgHeader struct {
	MsgID    string `json:"msg_id"`
	Username string `json:"username"`
	Session  string `json:"session"`
	MsgType  string `json:"msg_type"`
}

// ComposedMsg represents an entire message in a high-level structure.
type ComposedMsg struct {
	Header       MsgHeader
	ParentHeader MsgHeader
	Metadata     map[string]interface{}
	Content      interface{}
}

// InvalidSignatureError is returned when the signature on a received message does not
// validate.
type InvalidSignatureError struct{}

func (e *InvalidSignatureError) Error() string {
	return "A message had an invalid signature"
}

// WireMsgToComposedMsg translates a multipart ZMQ messages received from a socket into
// a ComposedMsg struct and a slice of return identities. This includes verifying the
// message signature.
func WireMsgToComposedMsg(msgparts [][]byte, signkey []byte) (ComposedMsg, [][]byte, error) {

	i := 0
	for string(msgparts[i]) != "<IDS|MSG>" {
		i++
	}
	identities := msgparts[:i]

	// Validate signature
	var msg ComposedMsg
	if len(signkey) != 0 {
		mac := hmac.New(sha256.New, signkey)
		for _, msgpart := range msgparts[i+2 : i+6] {
			mac.Write(msgpart)
		}
		signature := make([]byte, hex.DecodedLen(len(msgparts[i+1])))
		hex.Decode(signature, msgparts[i+1])
		if !hmac.Equal(mac.Sum(nil), signature) {
			return msg, nil, &InvalidSignatureError{}
		}
	}
	json.Unmarshal(msgparts[i+2], &msg.Header)
	json.Unmarshal(msgparts[i+3], &msg.ParentHeader)
	json.Unmarshal(msgparts[i+4], &msg.Metadata)
	json.Unmarshal(msgparts[i+5], &msg.Content)
	return msg, identities, nil
}

// ToWireMsg translates a ComposedMsg into a multipart ZMQ message ready to send, and
// signs it. This does not add the return identities or the delimiter.
func (msg ComposedMsg) ToWireMsg(signkey []byte) ([][]byte, error) {

	msgparts := make([][]byte, 5)
	header, err := json.Marshal(msg.Header)
	if err != nil {
		return msgparts, errors.Wrap(err, "Could not marshal message header")
	}
	msgparts[1] = header

	parentHeader, err := json.Marshal(msg.ParentHeader)
	if err != nil {
		return msgparts, errors.Wrap(err, "Could not marshal parent header")
	}
	msgparts[2] = parentHeader

	if msg.Metadata == nil {
		msg.Metadata = make(map[string]interface{})
	}
	metadata, err := json.Marshal(msg.Metadata)
	if err != nil {
		return msgparts, errors.Wrap(err, "Could not marshal metadata")
	}
	msgparts[3] = metadata

	content, err := json.Marshal(msg.Content)
	if err != nil {
		return msgparts, errors.Wrap(err, "Could not marshal content")
	}
	msgparts[4] = content

	// Sign the message.
	if len(signkey) != 0 {
		mac := hmac.New(sha256.New, signkey)
		for _, msgpart := range msgparts[1:] {
			mac.Write(msgpart)
		}
		msgparts[0] = make([]byte, hex.EncodedLen(mac.Size()))
		hex.Encode(msgparts[0], mac.Sum(nil))
	}
	return msgparts, nil
}

// MsgReceipt represents a received message, its return identities, and the sockets for
// communication.
type MsgReceipt struct {
	Msg        ComposedMsg
	Identities [][]byte
	Sockets    SocketGroup
}

// SendResponse sends a message back to return identites of the received message.
func (receipt *MsgReceipt) SendResponse(socket *zmq.Socket, msg ComposedMsg) {

	socket.SendMultipart(receipt.Identities, zmq.SNDMORE)
	socket.Send([]byte("<IDS|MSG>"), zmq.SNDMORE)

	msgParts, err := msg.ToWireMsg(receipt.Sockets.Key)
	if err != nil {
		log.Fatalln(err)
	}
	socket.SendMultipart(msgParts, 0)
	logger.Println("<--", msg.Header.MsgType)
	logger.Printf("%+v\n", msg.Content)
}

// NewMsg creates a new ComposedMsg to respond to a parent message. This includes setting
// up its headers.
func NewMsg(msgType string, parent ComposedMsg) (msg ComposedMsg) {
	msg.ParentHeader = parent.Header
	msg.Header.Session = parent.Header.Session
	msg.Header.Username = parent.Header.Username
	msg.Header.MsgType = msgType
	u, err := uuid.NewV4()
	if err != nil {
		log.Fatalln(errors.Wrap(err, "Could not generate UUID"))
	}
	msg.Header.MsgID = u.String()
	return
}
