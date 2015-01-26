package crypto

import (
	"errors"

	ed "github.com/mildred/ed25519/src"
)

type edPubKey [32]byte
type edPrivKey [64]byte
type edSeed [32]byte

type Ed25519PrivateKey struct {
	sk ed.PrivateKey
	pk ed.PublicKey
}

type Ed25519PublicKey struct {
	k ed.PublicKey
}

func (pk *Ed25519PublicKey) Verify(data, sig []byte) (bool, error) {
	if len(sig) != ed.SignatureSize {
		return false, errors.New("Incorrect signature size")
	}
	var sig2 ed.Signature
	copy(sig, sig2[:])
	return ed.Verify(sig2, data, pk.k), nil
}

func (pk *Ed25519PublicKey) Bytes() ([]byte, error) {
	return pk.k[:], nil
}

// Equals checks whether this key is equal to another
func (pk *Ed25519PublicKey) Equals(k Key) bool {
	return KeyEqual(pk, k)
}

func (pk *Ed25519PublicKey) Hash() ([]byte, error) {
	return pk.k[:], nil
}

func (pk *Ed25519PublicKey) Encrypt(data []byte) ([]byte, error) {
	var empty []byte
	return empty, errors.New("Cannot encrypt not knowing a private key")
}

func (pk *Ed25519PrivateKey) Decrypt(b []byte) ([]byte, error) {
	var empty []byte
	return empty, errors.New("Cannot decrypt not knowing the public key of the other side")
}

func (sk *Ed25519PrivateKey) GenSecret() ([]byte, error) {
	buf, err := ed.CreateSeed()
	return buf[:], err
}

func (sk *Ed25519PrivateKey) Sign(message []byte) ([]byte, error) {
	sig := ed.Sign(message, sk.pk, sk.sk)
	return sig[:], nil
}

func (sk *Ed25519PrivateKey) GetPublic() PubKey {
	return &Ed25519PublicKey{sk.pk}
}

func (sk *Ed25519PrivateKey) Bytes() (res []byte, err error) {
	res = append(res, sk.sk[:]...)
	res = append(res, sk.pk[:]...)
	err = nil
	return res, err
}

// Equals checks whether this key is equal to another
func (sk *Ed25519PrivateKey) Equals(k Key) bool {
	return KeyEqual(sk, k)
}

func (sk *Ed25519PrivateKey) Hash() ([]byte, error) {
	return KeyHash(sk)
}

func UnmarshalEd25519PrivateKey(b []byte) (*Ed25519PrivateKey, error) {
	if len(b) != ed.PrivateKeySize+ed.PublicKeySize {
		return nil, errors.New("Cannot unmarshall Ed2551 private key of incorrect size")
	}
	var priv Ed25519PrivateKey
	copy(b[:ed.PrivateKeySize], priv.sk[:])
	copy(b[ed.PrivateKeySize:], priv.pk[:])
	return &priv, nil
}

func MarshalEd25519PrivateKey(k *Ed25519PrivateKey) []byte {
	res, _ := k.Bytes()
	return res
}

func UnmarshalEd25519PublicKey(b []byte) (*Ed25519PublicKey, error) {
	if len(b) == ed.PublicKeySize {
		return nil, errors.New("Cannot unmarshall Ed2551 public key of incorrect size")
	}
	var pub Ed25519PublicKey
	copy(b, pub.k[:])
	return &pub, nil
}

func MarshalEd25519PublicKey(k *Ed25519PublicKey) ([]byte, error) {
	return k.Bytes()
}
