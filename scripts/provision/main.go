package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"time"

	"github.com/yandex-cloud/go-genproto/yandex/cloud/access"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/iam/v1"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/iam/v1/awscompatibility"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/lockbox/v1"
	"github.com/yandex-cloud/go-genproto/yandex/cloud/storage/v1"
	ycsdk "github.com/yandex-cloud/go-sdk"
	"gopkg.in/yaml.v2"
)

const (
	LockboxSecretKeyAccessKeyId     = "access_key_id"
	LockboxSecretKeySecretAccessKey = "secret_access_key"

	CreatedByLabelKey   = "created_by"
	CreatedByLabelValue = "yc-terraform-templater"
)

var (
	YcIamToken = mustEnv("YC_TOKEN")
	TfDir      = mustEnv("TF_DIR")
)

func main() {
	conf, err := SetupConf()
	if err != nil {
		log.Fatal(err)
	}
	sdk, err := ycsdk.Build(context.Background(), ycsdk.Config{
		Credentials: ycsdk.NewIAMTokenCredentials(YcIamToken),
	})
	if err != nil {
		log.Fatal(err)
	}

	tf := NewTerraformTemplater(sdk, conf)

	bucketName, err := tf.CreateBucket()
	if err != nil {
		log.Fatal(err)
	}

	saId, err := tf.CreateServiceAccount()
	if err != nil {
		log.Fatal(err)
	}

	if err := tf.AssignSaFolderStorageRoles(saId); err != nil {
		log.Fatal(err)
	}

	m, err := tf.CreateStaticKey(saId)
	if err != nil {
		log.Fatal(err)
	}

	lockboxSecretId, err := tf.CreateLockboxSecret(m)
	if err != nil {
		log.Fatal(err)
	}

	_ = m

	stateFile, err := os.OpenFile(path.Join(TfDir, "state.yaml"), os.O_WRONLY|os.O_CREATE, 0775)
	if err != nil {
		log.Fatalf("failed to write about Terraform state: %v", err)
	}
	defer stateFile.Close()
	accessKeyFile, err := os.OpenFile(path.Join(TfDir, "access-key.yaml"), os.O_WRONLY|os.O_CREATE, 0775)
	if err != nil {
		log.Fatalf("failed to write about Terraform state: %v", err)
	}
	defer accessKeyFile.Close()

	out := &StateOutput{
		StateBucket:     bucketName,
		SaId:            saId,
		LockboxSecretId: lockboxSecretId,
		LockboxSecretKeys: LockboxSecretKeys{
			AccessKeyId:     LockboxSecretKeyAccessKeyId,
			SecretAccessKey: LockboxSecretKeySecretAccessKey,
		},
	}
	if err := yaml.NewEncoder(stateFile).Encode(out); err != nil {
		log.Fatalf("failed to marshal state yaml output: %v", err)
	}
	fmt.Printf("AccessKeyId: %v\n", m[LockboxSecretKeyAccessKeyId])
	fmt.Printf("SecretAccessKey: %v\n", m[LockboxSecretKeySecretAccessKey])

	if err := yaml.NewEncoder(accessKeyFile).Encode(&LockboxSecretKeys{
		AccessKeyId:     m[LockboxSecretKeyAccessKeyId],
		SecretAccessKey: m[LockboxSecretKeySecretAccessKey],
	}); err != nil {
		log.Fatalf("failed to marshal access-key yaml output: %v", err)
	}
}

func SetupConf() (Config, error) {
	var conf Config
	confBytes, err := os.ReadFile(path.Join(TfDir, "config.yaml"))
	if err != nil {
		return conf, err
	}
	err = yaml.Unmarshal(confBytes, &conf)
	if err != nil {
		return conf, err
	}

	var errs []error
	if conf.Name == "" {
		errs = append(errs, errors.New(`"name" field in a config can't be empty`))
	}
	if conf.FolderId == "" {
		errs = append(errs, errors.New(`"folderId" field in a config can't be empty`))
	}
	if len(errs) > 0 {
		return conf, errors.Join(errs...)
	}

	return conf, nil
}

type Config struct {
	// Provisioned Terraform resources name.
	Name string `yaml:"name"`
	// Cloud folder id.
	FolderId string `yaml:"folderId"`
}

type StateOutput struct {
	// State bucket name.
	StateBucket string `yaml:"stateBucket"`
	// Bucket viewer/uploader service account id.
	SaId string `yaml:"saId"`
	// Id of lockbox secret with bucket credentials.
	LockboxSecretId string `yaml:"lockboxSecretId"`
	// Lockbox secret keys.
	LockboxSecretKeys LockboxSecretKeys `yaml:"secretKeys"`
}

type LockboxSecretKeys struct {
	AccessKeyId     string `yaml:"accessKeyId"`
	SecretAccessKey string `yaml:"secretAccessKey"`
}

func mustEnv(key string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	panic(fmt.Sprintf(`ENV "%s" must be set.`, key))
}

func randId() string {
	idBytes := make([]byte, 6)
	_, _ = rand.Read(idBytes)
	dst := make([]byte, base32.StdEncoding.EncodedLen(len(idBytes)))
	base32.StdEncoding.Encode(dst, idBytes)

	return string(bytes.ToLower(bytes.TrimRight(dst, "=")))
}

type TerraformTemplater struct {
	conf                Config
	standardStepTimeout time.Duration
	sdk                 *ycsdk.SDK
}

func NewTerraformTemplater(sdk *ycsdk.SDK, conf Config) *TerraformTemplater {
	return &TerraformTemplater{sdk: sdk, conf: conf, standardStepTimeout: 10 * time.Second}
}

func (t *TerraformTemplater) CreateBucket() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.standardStepTimeout)
	defer cancel()
	op, err := t.sdk.StorageAPI().Bucket().Create(ctx, &storage.CreateBucketRequest{
		Name:     fmt.Sprintf("%s-tf-state-%s", t.conf.Name, randId()),
		FolderId: t.conf.FolderId,
		Tags: []*storage.Tag{
			{
				Key:   CreatedByLabelKey,
				Value: CreatedByLabelValue,
			},
		},
	})
	if err != nil {
		return "", err
	}

	var b storage.Bucket
	if err := op.GetResponse().UnmarshalTo(&b); err != nil {
		return "", err
	}

	bucketName := b.GetName()

	log.Printf("Created bucket: name=%s", bucketName)

	return bucketName, nil
}

func (t *TerraformTemplater) CreateServiceAccount() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.standardStepTimeout)
	defer cancel()
	op, err := t.sdk.IAM().ServiceAccount().Create(ctx, &iam.CreateServiceAccountRequest{
		FolderId:    t.conf.FolderId,
		Name:        fmt.Sprintf("%s-tf-state-manager", t.conf.Name),
		Description: fmt.Sprintf(`"%s" SA created for uploading terraform state to bucket bucket`, t.conf.Name),
		Labels: map[string]string{
			CreatedByLabelKey: CreatedByLabelValue,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to run service account create operation: %v", err)
	}

	var m iam.CreateServiceAccountMetadata
	if err := op.GetMetadata().UnmarshalTo(&m); err != nil {
		return "", fmt.Errorf("failed to unmarshal create service account operation response: %v", err)
	}

	log.Printf(`Created service account for Terraform state bucket management: id="%s", name="%s"`, m.ServiceAccountId, t.conf.Name)

	return m.ServiceAccountId, nil
}

func (t *TerraformTemplater) AssignSaFolderStorageRoles(saId string) error {
	ctx, cancel := context.WithTimeout(context.Background(), t.standardStepTimeout)
	defer cancel()

	updateAccessBindingsReq := &access.UpdateAccessBindingsRequest{
		ResourceId: t.conf.FolderId,
		AccessBindingDeltas: []*access.AccessBindingDelta{
			{
				Action: access.AccessBindingAction_ADD,
				AccessBinding: &access.AccessBinding{
					RoleId: "storage.viewer",
					Subject: &access.Subject{
						Id:   saId,
						Type: "serviceAccount",
					},
				},
			},
			{
				Action: access.AccessBindingAction_ADD,
				AccessBinding: &access.AccessBinding{
					RoleId: "storage.uploader",
					Subject: &access.Subject{
						Id:   saId,
						Type: "serviceAccount",
					},
				},
			},
		},
	}

	_, err := t.sdk.ResourceManager().Folder().UpdateAccessBindings(ctx, updateAccessBindingsReq)
	if err != nil {
		return fmt.Errorf(`failed to assign "storage.{viewer,uploader}" roles to service account "%s-tf-state-manager": %v`, err)
	}

	log.Printf(`Assigned "storage.{viewer,uploader}" roles to service account "%s" on folder "%s"`, fmt.Sprintf("%s-tf-state-manager", t.conf.Name), t.conf.FolderId)

	return nil
}

func (t *TerraformTemplater) CreateStaticKey(saId string) (map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.standardStepTimeout)
	defer cancel()

	op, err := t.sdk.IAM().AWSCompatibility().AccessKey().Create(ctx, &awscompatibility.CreateAccessKeyRequest{
		ServiceAccountId: saId,
		Description:      fmt.Sprintf(`access key for sa "%s-tf-state-manager" to manage Terraform state bucket`, t.conf.Name),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create access key: %v", err)
	}

	log.Print("created access key for sa")

	return map[string]string{
		LockboxSecretKeyAccessKeyId:     op.AccessKey.KeyId,
		LockboxSecretKeySecretAccessKey: op.Secret,
	}, nil
}

func (t *TerraformTemplater) CreateLockboxSecret(m map[string]string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.standardStepTimeout)
	defer cancel()

	createSecretReq := lockbox.CreateSecretRequest{
		FolderId:    t.conf.FolderId,
		Name:        fmt.Sprintf("%s-tf-state-manager-sa-access-key", t.conf.Name),
		Description: fmt.Sprintf("%s-tf-state-manager service account AWS access key", t.conf.Name),
		Labels: map[string]string{
			CreatedByLabelKey: CreatedByLabelValue,
		},
		VersionPayloadEntries: []*lockbox.PayloadEntryChange{
			{
				Key: LockboxSecretKeyAccessKeyId,
				Value: &lockbox.PayloadEntryChange_TextValue{
					TextValue: m[LockboxSecretKeyAccessKeyId],
				},
			},
			{
				Key: LockboxSecretKeySecretAccessKey,
				Value: &lockbox.PayloadEntryChange_TextValue{
					TextValue: m[LockboxSecretKeySecretAccessKey],
				},
			},
		},
	}
	op, err := t.sdk.LockboxSecret().Secret().Create(ctx, &createSecretReq)
	if err != nil {
		return "", fmt.Errorf("failed to create lockbox secret: %v", err)
	}

	var meta lockbox.CreateSecretMetadata
	if err := op.GetMetadata().UnmarshalTo(&meta); err != nil {
		return "", err
	}

	log.Print("created lockbox secret for sa access key")
	return meta.SecretId, nil
}
