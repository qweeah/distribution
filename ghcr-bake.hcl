group "default" {
  targets = ["image-multi-arch"]
}

// Special target: https://github.com/docker/metadata-action#bake-definition
target "docker-metadata-action" {
  tags = ["ghcr.io/oras-project/registry:latest"]
}

target "image" {
  inherits = ["docker-metadata-action"]
}

target "image-multi-arch" {
  inherits = ["image"]
  platforms = [
    "linux/amd64",
    "linux/arm/v6",
    "linux/arm/v7",
    "linux/arm64",
    "linux/ppc64le",
    "linux/s390x"
  ]
}
