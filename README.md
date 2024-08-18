# sync-release

## Setup

To sync releases from one GitHub repository to Aliyun OSS, you should configure some environment variables. For easy use the
`sync-release` command will read the environment variables from a `.env` file in the current directory. You can copy the example
file `.env-dist` to `.env` and fill in the values:

```shell
cp .env-dist .env

# Fill in the values in the .env file
```

## Build

To build the `sync-release` command, you can run the following command:

```shell
go build -trimpath -ldflags '-s -w -buildid='
```

## Usage

```shell
# bellsoft-liberica buildpack
./sync-release -url https://github.com/paketo-buildpacks/bellsoft-liberica/raw/main/buildpack.toml

# syft buildpack
./sync-release -url https://github.com/paketo-buildpacks/syft/raw/main/buildpack.toml
## for a specific version
./sync-release -url https://github.com/paketo-buildpacks/syft/raw/a97ba9480643edd918f8da7c6a73bd7d06602340/buildpack.toml
```
