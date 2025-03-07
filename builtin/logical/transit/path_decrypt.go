package transit

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/helper/errutil"
	"github.com/hashicorp/vault/sdk/helper/keysutil"
	"github.com/hashicorp/vault/sdk/logical"
)

type DecryptBatchResponseItem struct {
	// Plaintext for the ciphertext present in the corresponding batch
	// request item
	Plaintext string `json:"plaintext" structs:"plaintext" mapstructure:"plaintext"`

	// Error, if set represents a failure encountered while encrypting a
	// corresponding batch request item
	Error string `json:"error,omitempty" structs:"error" mapstructure:"error"`
}

func (b *backend) pathDecrypt() *framework.Path {
	return &framework.Path{
		Pattern: "decrypt/" + framework.GenericNameRegex("name"),
		Fields: map[string]*framework.FieldSchema{
			"name": {
				Type:        framework.TypeString,
				Description: "Name of the policy",
			},

			"ciphertext": {
				Type: framework.TypeString,
				Description: `
The ciphertext to decrypt, provided as returned by encrypt.`,
			},

			"context": {
				Type: framework.TypeString,
				Description: `
Base64 encoded context for key derivation. Required if key derivation is
enabled.`,
			},

			"nonce": {
				Type: framework.TypeString,
				Description: `
Base64 encoded nonce value used during encryption. Must be provided if
convergent encryption is enabled for this key and the key was generated with
Vault 0.6.1. Not required for keys created in 0.6.2+.`,
			},
		},

		Callbacks: map[logical.Operation]framework.OperationFunc{
			logical.UpdateOperation: b.pathDecryptWrite,
		},

		HelpSynopsis:    pathDecryptHelpSyn,
		HelpDescription: pathDecryptHelpDesc,
	}
}

func (b *backend) pathDecryptWrite(ctx context.Context, req *logical.Request, d *framework.FieldData) (*logical.Response, error) {
	batchInputRaw := d.Raw["batch_input"]
	var batchInputItems []BatchRequestItem
	var err error
	if batchInputRaw != nil {
		err = decodeBatchRequestItems(batchInputRaw, &batchInputItems)
		if err != nil {
			return nil, fmt.Errorf("failed to parse batch input: %w", err)
		}

		if len(batchInputItems) == 0 {
			return logical.ErrorResponse("missing batch input to process"), logical.ErrInvalidRequest
		}
	} else {
		ciphertext := d.Get("ciphertext").(string)
		if len(ciphertext) == 0 {
			return logical.ErrorResponse("missing ciphertext to decrypt"), logical.ErrInvalidRequest
		}

		batchInputItems = make([]BatchRequestItem, 1)
		batchInputItems[0] = BatchRequestItem{
			Ciphertext: ciphertext,
			Context:    d.Get("context").(string),
			Nonce:      d.Get("nonce").(string),
		}
	}

	batchResponseItems := make([]DecryptBatchResponseItem, len(batchInputItems))
	contextSet := len(batchInputItems[0].Context) != 0

	userErrorInBatch := false
	internalErrorInBatch := false

	for i, item := range batchInputItems {
		if (len(item.Context) == 0 && contextSet) || (len(item.Context) != 0 && !contextSet) {
			return logical.ErrorResponse("context should be set either in all the request blocks or in none"), logical.ErrInvalidRequest
		}

		if item.Ciphertext == "" {
			userErrorInBatch = true
			batchResponseItems[i].Error = "missing ciphertext to decrypt"
			continue
		}

		// Decode the context
		if len(item.Context) != 0 {
			batchInputItems[i].DecodedContext, err = base64.StdEncoding.DecodeString(item.Context)
			if err != nil {
				userErrorInBatch = true
				batchResponseItems[i].Error = err.Error()
				continue
			}
		}

		// Decode the nonce
		if len(item.Nonce) != 0 {
			batchInputItems[i].DecodedNonce, err = base64.StdEncoding.DecodeString(item.Nonce)
			if err != nil {
				userErrorInBatch = true
				batchResponseItems[i].Error = err.Error()
				continue
			}
		}
	}

	// Get the policy
	p, _, err := b.GetPolicy(ctx, keysutil.PolicyRequest{
		Storage: req.Storage,
		Name:    d.Get("name").(string),
	}, b.GetRandomReader())
	if err != nil {
		return nil, err
	}
	if p == nil {
		return logical.ErrorResponse("encryption key not found"), logical.ErrInvalidRequest
	}
	if !b.System().CachingDisabled() {
		p.Lock(false)
	}

	for i, item := range batchInputItems {
		if batchResponseItems[i].Error != "" {
			continue
		}

		plaintext, err := p.Decrypt(item.DecodedContext, item.DecodedNonce, item.Ciphertext)
		if err != nil {
			switch err.(type) {
			case errutil.InternalError:
				internalErrorInBatch = true
			default:
				userErrorInBatch = true
			}
			batchResponseItems[i].Error = err.Error()
			continue
		}
		batchResponseItems[i].Plaintext = plaintext
	}

	resp := &logical.Response{}
	if batchInputRaw != nil {
		resp.Data = map[string]interface{}{
			"batch_results": batchResponseItems,
		}
	} else {
		if batchResponseItems[0].Error != "" {
			p.Unlock()

			if internalErrorInBatch {
				return nil, errutil.InternalError{Err: batchResponseItems[0].Error}
			}

			return logical.ErrorResponse(batchResponseItems[0].Error), logical.ErrInvalidRequest
		}
		resp.Data = map[string]interface{}{
			"plaintext": batchResponseItems[0].Plaintext,
		}
	}

	p.Unlock()

	// Depending on the errors in the batch, different status codes should be returned. User errors
	// will return a 400 and precede internal errors which return a 500. The reasoning behind this is
	// that user errors are non-retryable without making changes to the request, and should be surfaced
	// to the user first.
	switch {
	case userErrorInBatch:
		return logical.RespondWithStatusCode(resp, req, http.StatusBadRequest)
	case internalErrorInBatch:
		return logical.RespondWithStatusCode(resp, req, http.StatusInternalServerError)
	}

	return resp, nil
}

const pathDecryptHelpSyn = `Decrypt a ciphertext value using a named key`

const pathDecryptHelpDesc = `
This path uses the named key from the request path to decrypt a user
provided ciphertext. The plaintext is returned base64 encoded.
`
