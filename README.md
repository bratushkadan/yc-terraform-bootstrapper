# Template to setup Yandex Cloud project

## Roadmap

- [ ] Provision resources for Terraform bucket
- [ ] Packing into binary instructions & distribution
- [ ] Cobra
- [ ] re-create `terraform/access-key.yaml` from `terraform/state.yaml`
- [ ] list resources from "state.yaml"

Lower priority:
- [ ] dry run
- [ ] dangling resources cleanup


## Steps

1. Fill up the "terraform/config.yaml" config.
2. Run `cd scripts && YC_TOKEN=$(yc iam create-token) TF_DIR=../terraform go run ./cmd/main.go`

