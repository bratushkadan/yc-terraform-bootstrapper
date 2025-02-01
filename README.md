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
2. Run: 
```sh
TF_DIR=./terraform YC_TOKEN=$(./terraform/token) yc-terraform-bootstrapper 
```

## Use repo as starter

1. [Build](./#build)
2. Run the command:

```sh
rm -rf ./scripts
```

## Build

```
cd scripts
BUILD_BIN_PATH=$(mktemp)
CGO_ENABLED=0 go build -o "${BUILD_BIN_PATH}" ./...
sudo mv -f "${BUILD_BIN_PATH}" /usr/local/bin/yc-terraform-bootstrapper
cd ../
```
