package keymagic

import (
	"bufio"
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"github.com/quan-to/remote-signer"
	"github.com/quan-to/remote-signer/SLog"
	"github.com/quan-to/remote-signer/etc"
	"github.com/quan-to/remote-signer/keyBackend"
	"github.com/quan-to/remote-signer/models"
	"github.com/quan-to/remote-signer/openpgp"
	"golang.org/x/crypto/openpgp/armor"
	"golang.org/x/crypto/openpgp/packet"
	"io"
	"io/ioutil"
	"path"
	"strings"
	"sync"
	"time"
)

const MinKeyBits = 2048 // Should be safe until we have decent Quantum Computers

var pgpLog = SLog.Scope("PGPManager")

type PGPManager struct {
	sync.Mutex
	KeysBase64Encoded    bool
	keyIdentity          map[string][]*openpgp.Identity
	decryptedPrivateKeys map[string]*packet.PrivateKey
	entities             map[string]*openpgp.Entity
	fp8to16              map[string]string
	subKeyToKey          map[string]string
	krm                  etc.KRMInterface
	kbkend               keyBackend.Backend
}

func MakePGPManagerWithKRM(keyBackend keyBackend.Backend, krm etc.KRMInterface) etc.PGPInterface {
	if keyBackend == nil {
		pgpLog.Fatal("No keyBackend specified")
	}

	return &PGPManager{
		kbkend:               keyBackend,
		KeysBase64Encoded:    remote_signer.KeysBase64Encoded,
		keyIdentity:          make(map[string][]*openpgp.Identity),
		decryptedPrivateKeys: make(map[string]*packet.PrivateKey),
		entities:             make(map[string]*openpgp.Entity),
		fp8to16:              make(map[string]string),
		subKeyToKey:          make(map[string]string),
		krm:                  krm,
	}
}

func (pm *PGPManager) MinKeyBits() int {
	return MinKeyBits
}

func (pm *PGPManager) LoadKeys() {
	pm.Lock()
	defer pm.Unlock()

	pgpLog.Info("Loading keys from %s -> %s", pm.kbkend.Name(), pm.kbkend.Path())

	files, err := pm.kbkend.List()
	if err != nil {
		pgpLog.Fatal("Error listing keys: %s", err)
	}

	keysLoaded := 0

	for _, file := range files {
		pgpLog.Info("Loading key %s", file)
		keyData, m, err := pm.kbkend.Read(file)
		if err != nil {
			pgpLog.Error("Error loading key %s: %s", file, err)
			continue
		}

		if pm.KeysBase64Encoded {
			b, err := base64.StdEncoding.DecodeString(keyData)
			if err != nil {
				pgpLog.Error("Error base64 decoding %s: %s", file, err)
				continue
			}
			keyData = string(b)
		}

		err, kl := pm.LoadKeyWithMetadata(keyData, m)
		if err != nil {
			pgpLog.Error("Error decoding key %s: %s", file, err)
			continue
		}

		keysLoaded += kl
	}

	pgpLog.Info("Loaded %d private keys.", keysLoaded)
}

func (pm *PGPManager) LoadKeyWithMetadata(armoredKey, metadata string) (error, int) {
	err, n := pm.LoadKey(armoredKey)

	if err != nil {
		return err, n
	}

	fp, err := remote_signer.GetFingerPrintFromKey(armoredKey)
	if err != nil {
		pgpLog.Error("Cannot get fingerprint from key: %s", err)
		return nil, n
	}

	if metadata != "" {
		var meta map[string]string
		err = json.Unmarshal([]byte(metadata), &meta)
		if err != nil {
			pgpLog.Error("Error decoding metadata: %s", err)
			return nil, n
		}

		if meta["password"] != "" {
			err = pm.unlockKey(fp, meta["password"])
			if err != nil {
				pgpLog.Error("Cannot unlock key %s using metadata: %s", fp, err)
				return nil, n
			}
			pgpLog.Debug("Key %s unlocked using metadata.", fp)
			return nil, n
		}
	}

	pgpLog.Debug("No metadata for key %s. Skipping unlock...", fp)

	return nil, n
}

func (pm *PGPManager) SetKeysBase64Encoded(k bool) {
	pm.KeysBase64Encoded = k
}

func (pm *PGPManager) LoadKey(armoredKey string) (error, int) {
	keysLoaded := 0
	kr := strings.NewReader(armoredKey)
	keys, err := openpgp.ReadArmoredKeyRing(kr)
	if err != nil {
		return err, 0
	}

	for _, key := range keys {
		if key.PrivateKey != nil {
			fp := remote_signer.ByteFingerPrint2FP16(key.PrimaryKey.Fingerprint[:])
			ids := make([]*openpgp.Identity, 0)
			for _, v := range key.Identities {
				// Get only first
				c := *v // copy
				ids = append(ids, &c)
			}
			pm.keyIdentity[fp] = ids
			pm.fp8to16[fp[8:]] = fp
			pm.entities[fp] = key
			pgpLog.Info("Loaded private key %s", fp)

			for _, sub := range key.Subkeys {
				subKeyFp := remote_signer.IssuerKeyIdToFP16(sub.PublicKey.KeyId)
				pgpLog.Info("	Loaded subkey %s for %s", subKeyFp, fp)
				pm.subKeyToKey[subKeyFp] = fp
			}

			pm.krm.AddKey(key, true) // Add sticky public keys

			keysLoaded++
		}
	}

	return nil, keysLoaded
}

func (pm *PGPManager) sanitizeFingerprint(fp string) string {
	if len(fp) > 16 {
		fp = fp[len(fp)-16:]
	}
	if len(fp) == 8 {
		fp = pm.fp8to16[fp]
	}
	if len(fp) != 16 {
		pgpLog.Fatal("Cannot find key or invalid fingerprint: %s", fp)
	}

	return fp
}

func (pm *PGPManager) FixFingerPrint(fp string) string {
	pm.Lock()
	defer pm.Unlock()

	return pm.sanitizeFingerprint(fp)
}

func (pm *PGPManager) IsKeyLocked(fp string) bool {
	pm.Lock()
	defer pm.Unlock()

	fp = pm.sanitizeFingerprint(fp)
	return pm.decryptedPrivateKeys[fp] != nil
}

func (pm *PGPManager) unlockKey(fp, password string) error {

	fp = pm.sanitizeFingerprint(fp)

	if pm.decryptedPrivateKeys[fp] != nil {
		pgpLog.Info("Key %s already unlocked.", fp)
		return nil
	}
	ent := pm.entities[fp]

	if ent == nil {
		pgpLog.Error("No such key with fingerprint %s", fp)
		return fmt.Errorf("no such key %s", fp)
	}

	pk := ent.PrivateKey

	if pk == nil {
		return errors.New(fmt.Sprintf("private key %s not found", fp))
	}

	vpk := *pk // Copy data, for safety (aka: not unlock key at encrypted keys list

	err := vpk.Decrypt([]byte(password))

	if err != nil {
		return err
	}

	z := pm.entities[fp]

	for _, kz := range z.Subkeys {
		subkeyfp := remote_signer.IssuerKeyIdToFP16(kz.PublicKey.KeyId)
		pgpLog.Info("		Decrypting subkey %s from %s", subkeyfp, fp)
		err := kz.PrivateKey.Decrypt([]byte(password))
		if err != nil {
			return err
		}
		pm.decryptedPrivateKeys[subkeyfp] = kz.PrivateKey
		pgpLog.Debug("		Creating virtual entity for subkey %s from %s", subkeyfp, fp)
		pm.entities[subkeyfp] = remote_signer.CreateEntityFromKeys(fmt.Sprintf("Subkey for %s", fp), "", "", 0, kz.PublicKey, kz.PrivateKey)
	}

	pm.decryptedPrivateKeys[fp] = &vpk

	return nil
}

func (pm *PGPManager) UnlockKey(fp, password string) error {
	pm.Lock()
	defer pm.Unlock()

	return pm.unlockKey(fp, password)
}

func (pm *PGPManager) GetLoadedPrivateKeys() []models.KeyInfo {
	keyInfos := make([]models.KeyInfo, 0)

	for k, e := range pm.entities {
		v := e.PrivateKey
		z, _ := v.BitLength()
		keyInfo := models.KeyInfo{
			FingerPrint:           k,
			Identifier:            remote_signer.SimpleIdentitiesToString(pm.keyIdentity[k]),
			Bits:                  int(z),
			ContainsPrivateKey:    true,
			PrivateKeyIsDecrypted: pm.decryptedPrivateKeys[k] != nil,
		}
		keyInfos = append(keyInfos, keyInfo)
	}

	return keyInfos
}

func (pm *PGPManager) SavePrivateKey(fingerPrint, armoredData string, password interface{}) error {
	filename := fmt.Sprintf("%s.key", fingerPrint)
	if pm.KeysBase64Encoded {
		filename = fmt.Sprintf("%s.b64", fingerPrint)
	}

	filePath := path.Join(remote_signer.PrivateKeyFolder, filename)

	pgpLog.Info("Saving private key at %s", filePath)

	data := []byte(armoredData)

	if pm.KeysBase64Encoded {
		pgpLog.Debug("Base64 Encoding enabled. Encoding private key.")
		data = []byte(base64.StdEncoding.EncodeToString(data))
	}
	metadataJson := ""
	if password != nil {
		metadata := map[string]string{}
		metadata["password"] = password.(string)
		mj, _ := json.Marshal(metadata)
		metadataJson = string(mj)
	}

	return pm.kbkend.SaveWithMetadata(fingerPrint, string(data), metadataJson)
}

func (pm *PGPManager) SignData(fingerPrint string, data []byte, hashAlgorithm crypto.Hash) (string, error) {
	fingerPrint = pm.sanitizeFingerprint(fingerPrint)
	pm.Lock()
	pk := pm.decryptedPrivateKeys[fingerPrint]

	if pk == nil {
		pm.Unlock()
		return "", errors.New(fmt.Sprintf("key %s is not decrypt or not loaded", fingerPrint))
	}

	vpk := *pk
	ent := *pm.entities[fingerPrint]
	ent.PrivateKey = &vpk
	pm.Unlock()

	d := bytes.NewReader(data)

	var b bytes.Buffer
	bw := bufio.NewWriter(&b)

	config := &packet.Config{
		DefaultHash: hashAlgorithm,
	}

	err := openpgp.ArmoredDetachSign(bw, &ent, d, config)
	if err != nil {
		return "", err
	}
	err = bw.Flush()
	if err != nil {
		return "", err
	}

	return b.String(), nil
}

func (pm *PGPManager) GetPublicKeyEntity(fingerPrint string) *openpgp.Entity {
	pm.Lock()
	defer pm.Unlock()
	fingerPrint = pm.sanitizeFingerprint(fingerPrint)

	ent := pm.entities[fingerPrint]

	if ent == nil {
		// Try fetch subkey
		subMaster := pm.subKeyToKey[fingerPrint]
		if len(subMaster) > 0 {
			ent = pm.entities[subMaster]
		} else {
			// Try PKS
			ent = pm.krm.GetKey(fingerPrint)
		}
	}

	if ent != nil {
		pm.entities[fingerPrint] = ent
	}

	return ent
}

func (pm *PGPManager) GetPublicKey(fingerPrint string) *packet.PublicKey {
	var pubKey *packet.PublicKey
	pm.Lock()
	defer pm.Unlock()
	fingerPrint = pm.sanitizeFingerprint(fingerPrint)

	ent := pm.entities[fingerPrint]

	if ent == nil {
		// Try fetch subkey
		subMaster := pm.subKeyToKey[fingerPrint]
		if len(subMaster) > 0 {
			ent = pm.entities[subMaster]
			pubKey = ent.PrimaryKey
		} else {
			// Try PKS
			ent = pm.krm.GetKey(fingerPrint)
		}
	}

	if ent != nil {
		pubKey = ent.PrimaryKey
		pm.entities[fingerPrint] = ent
	}

	return pubKey
}

func (pm *PGPManager) GetPublicKeyAscii(fingerPrint string) string {
	key := ""
	pubKey := pm.GetPublicKey(fingerPrint)
	ent := pm.GetPublicKeyEntity(fingerPrint)

	if ent != nil { // Try get full entity first
		serializedEntity := bytes.NewBuffer(nil)
		err := ent.Serialize(serializedEntity)

		if err != nil {
			return ""
		}

		buf := bytes.NewBuffer(nil)
		headers := map[string]string{
			"Version": "GnuPG v2",
			"Comment": "Generated by Quanto Remote Signer",
		}

		w, err := armor.Encode(buf, openpgp.PublicKeyType, headers)
		if err != nil {
			return ""
		}
		_, err = w.Write(serializedEntity.Bytes())
		if err != nil {
			return ""
		}
		err = w.Close()
		if err != nil {
			return ""
		}

		key = buf.String()
	} else if pubKey != nil { // If not, get just the public key
		serializedEntity := bytes.NewBuffer(nil)
		err := pubKey.Serialize(serializedEntity)

		if err != nil {
			return ""
		}

		buf := bytes.NewBuffer(nil)
		headers := map[string]string{
			"Version": "GnuPG v2",
			"Comment": "Generated by Quanto Remote Signer",
		}

		w, err := armor.Encode(buf, openpgp.PublicKeyType, headers)
		if err != nil {
			return ""
		}
		_, err = w.Write(serializedEntity.Bytes())
		if err != nil {
			return ""
		}
		err = w.Close()
		if err != nil {
			return ""
		}

		key = buf.String()
	}

	return key
}

func (pm *PGPManager) VerifySignatureStringData(data string, signature string) (bool, error) {
	return pm.VerifySignature([]byte(data), signature)
}

func (pm *PGPManager) VerifySignature(data []byte, signature string) (bool, error) {
	var issuerKeyId uint64
	var publicKey *packet.PublicKey
	var fingerPrint string

	signature = remote_signer.SignatureFix(signature)
	b := bytes.NewReader([]byte(signature))
	block, err := armor.Decode(b)
	if err != nil {
		return false, err
	}

	if block.Type != openpgp.SignatureType {
		return false, errors.New("openpgp packet is not signature")
	}

	reader := packet.NewReader(block.Body)
	for {
		pkt, err := reader.Next()

		if err != nil {
			return false, err
		}

		switch sig := pkt.(type) {
		case *packet.Signature:
			if sig.IssuerKeyId == nil {
				return false, errors.New("signature doesn't have an issuer")
			}
			issuerKeyId = *sig.IssuerKeyId
			fingerPrint = remote_signer.IssuerKeyIdToFP16(issuerKeyId)
		case *packet.SignatureV3:
			issuerKeyId = sig.IssuerKeyId
			fingerPrint = remote_signer.IssuerKeyIdToFP16(issuerKeyId)
		default:
			return false, errors.New("non signature packet found")
		}

		if len(fingerPrint) == 16 {
			publicKey = pm.GetPublicKey(fingerPrint)
			if publicKey != nil {
				break
			}
		}
	}

	if publicKey == nil {
		return false, errors.New("cannot find public key to verify signature")
	}

	keyRing := make(openpgp.EntityList, 1)
	keyRing[0] = pm.entities[fingerPrint]

	dr := bytes.NewReader(data)
	sr := strings.NewReader(signature)

	_, err = openpgp.CheckArmoredDetachedSignature(keyRing, dr, sr)

	if err != nil {
		return false, err
	}

	return true, nil
}

func (pm *PGPManager) GenerateTestKey() (string, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)

	if err != nil {
		return "", err
	}

	var cTimestamp = time.Now()

	pgpPubKey := packet.NewRSAPublicKey(cTimestamp, &privateKey.PublicKey)
	pgpPrivKey := packet.NewRSAPrivateKey(cTimestamp, privateKey)

	e := remote_signer.CreateEntityFromKeys("", "", "", 0, pgpPubKey, pgpPrivKey)

	serializedEntity := bytes.NewBuffer(nil)
	err = e.SerializePrivate(serializedEntity, &packet.Config{
		DefaultHash: crypto.SHA512,
	})

	if err != nil {
		return "", err
	}

	buf := bytes.NewBuffer(nil)
	headers := map[string]string{
		"Version": "GnuPG v2",
		"Comment": "Generated by Quanto Remote Signer",
	}

	w, err := armor.Encode(buf, openpgp.PrivateKeyType, headers)
	if err != nil {
		return "", err
	}
	_, err = w.Write(serializedEntity.Bytes())
	if err != nil {
		return "", err
	}
	err = w.Close()
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (pm *PGPManager) GeneratePGPKey(identifier, password string, numBits int) (string, error) {
	if numBits < MinKeyBits {
		return "", errors.New(fmt.Sprintf("dont generate RSA keys with less than %d, its not safe. try use 3072 or higher", MinKeyBits))
	}

	privateKey, err := rsa.GenerateKey(rand.Reader, numBits)

	if err != nil {
		return "", err
	}

	var cTimestamp = time.Now()

	pgpPubKey := packet.NewRSAPublicKey(cTimestamp, &privateKey.PublicKey)
	pgpPrivKey := packet.NewRSAPrivateKey(cTimestamp, privateKey)

	e := remote_signer.CreateEntityFromKeys(identifier, "", "", 0, pgpPubKey, pgpPrivKey)

	serializedEntity := bytes.NewBuffer(nil)
	err = e.SerializePrivate(serializedEntity, &packet.Config{
		DefaultHash: crypto.SHA512,
	})

	if err != nil {
		return "", err
	}

	buf := bytes.NewBuffer(nil)
	headers := map[string]string{
		"Version": "GnuPG v2",
		"Comment": "Generated by Quanto Remote Signer",
	}

	w, err := armor.Encode(buf, openpgp.PrivateKeyType, headers)
	if err != nil {
		return "", err
	}
	_, err = w.Write(serializedEntity.Bytes())
	if err != nil {
		return "", err
	}
	err = w.Close()
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (pm *PGPManager) Encrypt(filename, fingerPrint string, data []byte, dataOnly bool) (string, error) {
	var pubKey = pm.GetPublicKey(fingerPrint)

	if pubKey == nil {
		return "", fmt.Errorf("no public key for %s", fingerPrint)
	}
	fingerPrint = remote_signer.ByteFingerPrint2FP16(pubKey.Fingerprint[:])
	var entity *openpgp.Entity

	pm.Lock()
	entity = pm.entities[fingerPrint]
	pm.Unlock()

	buf := bytes.NewBuffer(nil)

	hints := &openpgp.FileHints{
		FileName: filename,
		IsBinary: true,
		ModTime:  time.Now(),
	}

	config := &packet.Config{
		DefaultHash:            crypto.SHA512,
		DefaultCipher:          packet.CipherAES256,
		DefaultCompressionAlgo: packet.CompressionZLIB,
		CompressionConfig: &packet.CompressionConfig{
			Level: 9,
		},
	}

	closer, err := openpgp.Encrypt(buf, []*openpgp.Entity{entity}, nil, hints, config)

	if err != nil {
		return "", err
	}

	_, err = closer.Write(data)

	if err != nil {
		return "", err
	}

	err = closer.Close()
	if err != nil {
		return "", err
	}

	encData := buf.Bytes()

	if dataOnly {
		return base64.StdEncoding.EncodeToString(encData), nil
	}

	buf = bytes.NewBuffer(nil)
	headers := map[string]string{
		"Version": "GnuPG v2",
		"Comment": "Generated by Quanto Remote Signer",
	}

	w, err := armor.Encode(buf, "PGP MESSAGE", headers)
	if err != nil {
		return "", err
	}
	_, err = w.Write(encData)
	if err != nil {
		return "", err
	}
	err = w.Close()
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func (pm *PGPManager) Decrypt(data string, dataOnly bool) (*models.GPGDecryptedData, error) {
	var err error
	var fps []string
	ret := &models.GPGDecryptedData{}

	if dataOnly {
		fps, err = remote_signer.GetFingerPrintsFromEncryptedMessageRaw(data)
	} else {
		fps, err = remote_signer.GetFingerPrintsFromEncryptedMessage(data)
	}

	if err != nil {
		return nil, err
	}

	if len(fps) == 0 {
		return nil, fmt.Errorf("no encrypted payloads found")
	}

	var decv *packet.PrivateKey
	var ent openpgp.Entity
	var subent *openpgp.Entity

	pm.Lock()
	for _, v := range fps {
		// Try directly
		decv = pm.decryptedPrivateKeys[v]
		if decv != nil {
			ent = *pm.entities[v]
			break
		}

		// Try subkeys
		subKeyMaster := pm.subKeyToKey[v]
		if len(subKeyMaster) > 0 {
			// Check if it is decrypted
			decv = pm.decryptedPrivateKeys[subKeyMaster]
			if decv != nil {
				ent = *pm.entities[subKeyMaster]
				subent = pm.entities[v]
				break
			}
		}
	}
	pm.Unlock()

	if decv == nil {
		return nil, fmt.Errorf("no unlocked key for decrypting packet")
	}

	keyRing := make(openpgp.EntityList, 1)
	ent.PrivateKey = decv
	keyRing[0] = &ent

	if subent != nil {
		keyRing[1] = subent
	}

	var rd io.Reader

	if dataOnly {
		d, err := base64.StdEncoding.DecodeString(data)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(d)
	} else {
		srd := strings.NewReader(data)
		p, err := armor.Decode(srd)
		if err != nil {
			return nil, err
		}

		rd = p.Body
	}

	md, err := openpgp.ReadMessage(rd, keyRing, nil, nil)

	if err != nil {
		return nil, err
	}

	rawData, err := ioutil.ReadAll(md.LiteralData.Body)

	if err != nil {
		return nil, err
	}

	ret.Base64Data = base64.StdEncoding.EncodeToString(rawData)
	ret.Filename = md.LiteralData.FileName

	return ret, nil
}

func (pm *PGPManager) GetCachedKeys() []models.KeyInfo {
	return pm.krm.GetCachedKeys()
}