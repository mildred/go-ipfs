package network

import (
	context "github.com/jbenet/go-ipfs/Godeps/_workspace/src/code.google.com/p/go.net/context"

	bsmsg "github.com/jbenet/go-ipfs/exchange/bitswap/message"
	netmsg "github.com/jbenet/go-ipfs/net/message"
	peer "github.com/jbenet/go-ipfs/peer"
)

// NetMessageAdapter wraps a NetMessage network service
func NetMessageAdapter(s NetMessageService, r Receiver) Adapter {
	adapter := impl{
		nms:      s,
		receiver: r,
	}
	s.SetHandler(&adapter)
	return &adapter
}

// implements an Adapter that integrates with a NetMessage network service
type impl struct {
	nms NetMessageService

	// inbound messages from the network are forwarded to the receiver
	receiver Receiver
}

// HandleMessage marshals and unmarshals net messages, forwarding them to the
// BitSwapMessage receiver
func (adapter *impl) HandleMessage(
	ctx context.Context, incoming netmsg.NetMessage) netmsg.NetMessage {

	if adapter.receiver == nil {
		return nil
	}

	received, err := bsmsg.FromNet(incoming)
	if err != nil {
		go adapter.receiver.ReceiveError(err)
		return nil
	}

	p, bsmsg := adapter.receiver.ReceiveMessage(ctx, incoming.Peer(), received)

	// TODO(brian): put this in a helper function
	if bsmsg == nil || p == nil {
		return nil
	}

	outgoing, err := bsmsg.ToNet(p)
	if err != nil {
		go adapter.receiver.ReceiveError(err)
		return nil
	}

	return outgoing
}

func (adapter *impl) SendMessage(
	ctx context.Context,
	p *peer.Peer,
	outgoing bsmsg.BitSwapMessage) error {

	nmsg, err := outgoing.ToNet(p)
	if err != nil {
		return err
	}
	return adapter.nms.SendMessage(ctx, nmsg)
}

func (adapter *impl) SendRequest(
	ctx context.Context,
	p *peer.Peer,
	outgoing bsmsg.BitSwapMessage) (bsmsg.BitSwapMessage, error) {

	outgoingMsg, err := outgoing.ToNet(p)
	if err != nil {
		return nil, err
	}
	incomingMsg, err := adapter.nms.SendRequest(ctx, outgoingMsg)
	if err != nil {
		return nil, err
	}
	return bsmsg.FromNet(incomingMsg)
}

func (adapter *impl) SetDelegate(r Receiver) {
	adapter.receiver = r
}
