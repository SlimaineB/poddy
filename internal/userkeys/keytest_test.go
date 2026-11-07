package userkeys

import (
	"crypto/ed25519"
	"crypto/rand"

	"golang.org/x/crypto/ssh"
)

func generateKey() (ssh.PublicKey, ssh.Signer, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	pub, err := ssh.NewPublicKey(public)
	if err != nil {
		return nil, nil, err
	}
	signer, err := ssh.NewSignerFromKey(private)
	return pub, signer, err
}

func sshMarshalPublic(public ssh.PublicKey) []byte {
	return ssh.MarshalAuthorizedKey(public)
}
