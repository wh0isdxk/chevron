package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/quan-to/chevron/QuantoError"
	"github.com/quan-to/chevron/models"
)

func TestKREGetKey(t *testing.T) {
	// region Test Get Key
	req, err := http.NewRequest("GET", "/keyRing/getKey", nil)
	q := req.URL.Query()
	q.Add("fingerPrint", testKeyFingerprint)
	req.URL.RawQuery = q.Encode()

	errorDie(err, t)

	res := executeRequest(req)

	d, err := ioutil.ReadAll(res.Body)

	errorDie(err, t)

	if res.Code != 200 {
		var errObj QuantoError.ErrorObject
		err := json.Unmarshal(d, &errObj)
		fmt.Println(errObj)
		errorDie(err, t)
		errorDie(fmt.Errorf(errObj.Message), t)
	}

	errorDie(err, t)
	// endregion
	// region Test Inexistent Get Key
	req, err = http.NewRequest("GET", "/keyRing/getKey", nil)
	q = req.URL.Query()
	q.Add("fingerPrint", "WOLOLO")
	req.URL.RawQuery = q.Encode()

	errorDie(err, t)

	res = executeRequest(req)

	errObj, err := ReadErrorObject(res.Body)

	errorDie(err, t)

	if errObj.ErrorCode != QuantoError.NotFound {
		errorDie(fmt.Errorf("expected error code %s got %s", QuantoError.NotFound, errObj.ErrorCode), t)
	}
	// endregion
}

func TestKREGetCachedKeys(t *testing.T) {
	// region Test Get Cached Keys
	req, err := http.NewRequest("GET", "/keyRing/cachedKeys", nil)

	errorDie(err, t)

	res := executeRequest(req)

	d, err := ioutil.ReadAll(res.Body)
	errorDie(err, t)

	if res.Code != 200 {
		var errObj QuantoError.ErrorObject
		err := json.Unmarshal(d, &errObj)
		fmt.Println(errObj)
		errorDie(err, t)
		errorDie(fmt.Errorf(errObj.Message), t)
	}

	var keyInfo []models.KeyInfo

	err = json.Unmarshal(d, &keyInfo)
	errorDie(err, t)

	found := false
	for _, v := range keyInfo {
		if v.FingerPrint == testKeyFingerprint {
			found = true
			break
		}
	}

	if !found {
		errorDie(fmt.Errorf("expected key %s to be in cached keys", testKeyFingerprint), t)
	}
	// endregion
}

func TestKREGetLoadedPrivateKeys(t *testing.T) {
	// region Test Get Loaded Private Keys
	req, err := http.NewRequest("GET", "/keyRing/privateKeys", nil)

	errorDie(err, t)

	res := executeRequest(req)

	d, err := ioutil.ReadAll(res.Body)
	errorDie(err, t)

	if res.Code != 200 {
		var errObj QuantoError.ErrorObject
		err := json.Unmarshal(d, &errObj)
		fmt.Println(errObj)
		errorDie(err, t)
		errorDie(fmt.Errorf(errObj.Message), t)
	}

	var keyInfo []models.KeyInfo

	err = json.Unmarshal(d, &keyInfo)
	errorDie(err, t)

	found := false
	for _, v := range keyInfo {
		if v.FingerPrint == testKeyFingerprint {
			found = true
			break
		}
	}

	if !found {
		errorDie(fmt.Errorf("expected key %s to be in private keys", testKeyFingerprint), t)
	}
	// endregion
}

func TestKREAddPrivateKey(t *testing.T) {
	ctx := context.Background()
	key, err := gpg.GenerateTestKey()
	errorDie(err, t)

	// Default Test Key Password is 1234

	// region Test Add Private Key
	payload := models.KeyRingAddPrivateKeyData{
		EncryptedPrivateKey: key,
		SaveToDisk:          true,
		Password:            "1234",
	}

	body, _ := json.Marshal(payload)

	r := bytes.NewReader(body)

	req, err := http.NewRequest("POST", "/keyRing/addPrivateKey", r)

	errorDie(err, t)

	res := executeRequest(req)

	d, err := ioutil.ReadAll(res.Body)
	errorDie(err, t)

	if res.Code != 200 {
		var errObj QuantoError.ErrorObject
		err := json.Unmarshal(d, &errObj)
		fmt.Println(errObj)
		errorDie(err, t)
		errorDie(fmt.Errorf(errObj.Message), t)
	}

	var retData models.GPGAddPrivateKeyReturn

	err = json.Unmarshal(d, &retData)
	errorDie(err, t)

	// endregion
	// region Test Add Private Key Invalid Password
	payload.Password = "HUEBR"

	body, _ = json.Marshal(payload)

	r = bytes.NewReader(body)

	req, err = http.NewRequest("POST", "/keyRing/addPrivateKey", r)

	errorDie(err, t)

	res = executeRequest(req)

	errObj, err := ReadErrorObject(res.Body)

	errorDie(err, t)

	if errObj.ErrorCode != QuantoError.InvalidFieldData {
		errorDie(fmt.Errorf("expected error code to be %s got %s", QuantoError.InvalidFieldData, errObj.ErrorCode), t)
	}
	// endregion
	// region Test Add Public Key as private
	payload.Password = ""
	payload.EncryptedPrivateKey, _ = gpg.GetPublicKeyAscii(ctx, testKeyFingerprint)

	body, _ = json.Marshal(payload)

	r = bytes.NewReader(body)

	req, err = http.NewRequest("POST", "/keyRing/addPrivateKey", r)

	errorDie(err, t)

	res = executeRequest(req)

	errObj, err = ReadErrorObject(res.Body)

	errorDie(err, t)

	if errObj.ErrorCode != QuantoError.NotFound {
		errorDie(fmt.Errorf("expected error code to be %s got %s", QuantoError.NotFound, errObj.ErrorCode), t)
	}
	// endregion
	// region Test Add Invalid ASCII
	payload.Password = ""
	payload.EncryptedPrivateKey = "uaheirohaih41oi23uh  ,//;;1 ééé"

	body, _ = json.Marshal(payload)

	r = bytes.NewReader(body)

	req, err = http.NewRequest("POST", "/keyRing/addPrivateKey", r)

	errorDie(err, t)

	res = executeRequest(req)

	errObj, err = ReadErrorObject(res.Body)

	errorDie(err, t)

	if errObj.ErrorCode != QuantoError.InvalidFieldData {
		errorDie(fmt.Errorf("expected error code to be %s got %s", QuantoError.InvalidFieldData, errObj.ErrorCode), t)
	}
	// endregion
}
