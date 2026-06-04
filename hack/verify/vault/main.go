/*
Copyright 2026 Keiailab.

Licensed under the MIT License. See the LICENSE file for details.
*/

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	mongodbv1alpha1 "github.com/keiailab/mongodb-operator/api/v1alpha1"
	encryptionpkg "github.com/keiailab/mongodb-operator/internal/controller/encryption"
)

func main() {
	// Start kubectl port-forward in background.
	pfCmd := exec.Command("kubectl", "--kubeconfig=/tmp/kind-mongo-kubeconfig", "-n", "external-systems",
		"port-forward", "svc/vault", "48200:8200")
	if err := pfCmd.Start(); err != nil {
		fmt.Printf("FAIL port-forward start: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = pfCmd.Process.Kill() }()
	time.Sleep(2 * time.Second)

	client, err := encryptionpkg.NewVaultClientFromSpec(
		&mongodbv1alpha1.VaultKMSConfig{Address: "http://127.0.0.1:48200", KeyName: "mongo-key"},
		"root-token-12345", nil)
	if err != nil {
		fmt.Printf("FAIL NewVaultClient: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.Health(ctx); err != nil {
		fmt.Printf("FAIL Health: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("✓ Vault.Health() PASS — real round-trip")

	plaintext := []byte("mongodb-encryption-keyfile-test-data!!")
	ct, err := client.Encrypt(ctx, "mongo-key", plaintext)
	if err != nil {
		fmt.Printf("FAIL Encrypt: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ Vault.Encrypt() PASS — ciphertext: %s\n", ct[:25]+"...")

	decrypted, err := client.Decrypt(ctx, "mongo-key", ct)
	if err != nil {
		fmt.Printf("FAIL Decrypt: %v\n", err)
		os.Exit(1)
	}
	if string(decrypted) != string(plaintext) {
		fmt.Printf("FAIL round-trip mismatch: want %q got %q\n", plaintext, decrypted)
		os.Exit(1)
	}
	fmt.Println("✓ Vault.Decrypt() PASS — plaintext matches")
	fmt.Println("✓ ALL REAL VAULT TRANSIT ROUND-TRIP TESTS PASS")
}
