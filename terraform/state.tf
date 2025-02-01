terraform {
  backend "s3" {
    endpoint   = "storage.yandexcloud.net"
    bucket     = "${BUCKET_NAME}"
    key        = "${BUCKET_NAME}-state.tfstate"
    region     = "us-east-1"
    access_key = "${ACCESS_KEY_ID}"

    skip_credentials_validation = true
    skip_metadata_api_check     = true
  }
}
