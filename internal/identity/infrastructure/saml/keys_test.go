package saml

import (
	"bytes"
	"testing"

	"github.com/crewjam/saml/xmlenc"
)

// TestOAEP256DecrypterRegistered 校验 XML Encryption 1.1 的 rsa-oaep 解密器已注册：
// Keycloak 26 默认用该算法加密断言，未注册时 ParseXMLResponse 会返回 "Authentication failed"。
func TestOAEP256DecrypterRegistered(t *testing.T) {
	certPEM, keyPEM, _ := testSPKeys(t)
	pair, err := ParseKeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("parse key pair: %v", err)
	}
	plaintext := []byte("<Assertion>encrypted payload</Assertion>")
	encrypted, err := xmlenc.OAEP_SHA256().Encrypt(pair.Certificate, plaintext, nil)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	found := false
	for _, method := range encrypted.FindElements(".//EncryptionMethod") {
		if method.SelectAttrValue("Algorithm", "") == "http://www.w3.org/2009/xmlenc11#rsa-oaep" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing rsa-oaep EncryptionMethod")
	}
	// Keycloak 26 把 DigestMethod 写成 XML Encryption 命名空间，需与库内置注册表兼容。
	digest := encrypted.FindElement("./KeyInfo/EncryptedKey/EncryptionMethod/DigestMethod")
	if digest == nil {
		t.Fatal("missing DigestMethod")
	}
	digest.CreateAttr("Algorithm", "http://www.w3.org/2001/04/xmlenc#sha256")
	decrypted, err := xmlenc.Decrypt(pair.Key, encrypted)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted payload mismatch")
	}
}
