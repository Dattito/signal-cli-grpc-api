# Dockerized Signal Messenger gRPC API
This project is forked from [signal-cli-rest-api](https://github.com/datti-to/signal-cli-grpc-api) to create a gRPC based version.

This project creates a small dockerized gRPC API around [signal-cli](https://github.com/AsamK/signal-cli).

At the moment, the following functionality is exposed via gRPC:

- Register a number
- Verify the number using the code received via SMS
- Send message (+ attachments) to multiple recipients (or a group)
- Receive messages
- Link devices
- Create/List/Remove groups
- List/Serve/Delete attachments
- Update profile

and many more

## Modes

The `signal-cli-rest-api` supports three different modes:

* Normal Mode: In normal mode, the `signal-cli` executable is invoked for every REST API request. As `signal-cli` is a Java application, a significant amount of time is spent in the JVM (Java Virtual Machine) startup - which makes this mode pretty slow.
* Native Mode: Instead of calling a Java executable for every REST API request, a native image (compiled with GraalVM) is called. This mode therefore usually performs better than the normal mode. 
* JSON-RPC Mode: In JSON-RPC mode, a single `signal-cli` instance is spawned in daemon mode. The communication happens via JSON-RPC. This mode is usually the fastest.


| architecture | normal mode | native mode | json-rpc mode |
|--------------|:-----------:|:-----------:|---------------|
|    x86-64    |      :heavy_check_mark:     |     :heavy_check_mark:      |       :heavy_check_mark:      |
|    armv7     |      :heavy_check_mark:     |     ❌ <sup>1</sup>     |       :heavy_check_mark:      |
|    arm64     |      :heavy_check_mark:     |     :heavy_check_mark:      |       :heavy_check_mark:      |


|     mode     | speed       |
|-------------:|:------------|
|    json-rpc  |    :heavy_check_mark: :heavy_check_mark: :heavy_check_mark: |
|    native    |    :heavy_check_mark: :heavy_check_mark:    |
|    normal    |    :heavy_check_mark:       |


Notes:
1. If the signal-cli-rest-api docker container is started on an armv7 system in native mode, it automatically falls back to the normal mode.

## Auto Receive Schedule

> :warning: This setting is only needed in normal/native mode!

[signal-cli](https://github.com/AsamK/signal-cli), which this REST API wrapper is based on, recommends to call `receive` on a regular basis. So, if you are not already calling the `receive` endpoint regularily, it is recommended to set the `AUTO_RECEIVE_SCHEDULE` parameter in the docker-compose.yml file. The `AUTO_RECEIVE_SCHEDULE` accepts cron schedule expressions and automatically calls the `receive` endpoint at the given time. e.g: `0 22 * * *` calls `receive` daily at 10pm. If you are not familiar with cron schedule expressions, you can use this [website](https://crontab.guru).

**WARNING** Calling `receive` will fetch all the messages for the registered Signal number from the Signal Server! So, if you are using the REST API for receiving messages, it's _not_ a good idea to use the `AUTO_RECEIVE_SCHEDULE` parameter, as you might lose some messages that way.

## Example

Sample `docker-compose.yml`file:

```yaml
version: "3"
services:
  signal-cli-grpc-api:
    image: dattito/signal-cli-grpc-api:latest
    environment:
      - MODE=normal #supported modes: json-rpc, native, normal
        #- AUTO_RECEIVE_SCHEDULE=0 22 * * * #enable this parameter on demand (see description below)
    ports:
      - "9090:9090" #map docker port 9090 to host port 9090.
    volumes:
      - "./signal-cli-config:/home/.local/share/signal-cli" #map "signal-cli-config" folder on host system into docker container. the folder contains the password and cryptographic keys when a new number is registered
```

## Documentation

### API Reference

The Swagger API documentation can be found [here](https://bbernhard.github.io/signal-cli-rest-api/). If you prefer a simple text file based API documentation have a look [here](https://github.com/bbernhard/signal-cli-rest-api/blob/master/doc/EXAMPLES.md).

### Blog Posts

[Running Signal Messenger REST API in Azure Web App for Containers](https://stefanstranger.github.io/2021/06/01/RunningSignalRESTAPIinAppService/) - written by [@stefanstranger](https://github.com/stefanstranger)


## Clients & Libraries

|     Name    | Client           | Library  | Language | Maintainer |
| ------------- |:-------------:| :-----:|:-----:|:-----:|
| [Shell Client](https://github.com/florian-h05/shell-script_collection/blob/main/signal-cli-rest-api_client.bash)      | X | | Shell | [@florian-h05](https://github.com/florian-h05)
| [pysignalclirestapi](https://pypi.org/project/pysignalclirestapi/)      | | X | Python | [@bbernhard](https://github.com/bbernhard)

In case you need more functionality, please **file a ticket** or **create a PR**.
