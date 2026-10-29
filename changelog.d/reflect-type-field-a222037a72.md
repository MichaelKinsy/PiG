### Fixed

- A Go program that embeds PiG and also links a reflection consumer such as protobuf is no longer about 6 MB larger because of `durable/internal/ordered`: its field metadata embedded `reflect.Type` anonymously, which promoted `MethodByName` onto it and made the linker keep every exported method of every reachable type; the field is now named. <!-- fix: a222037a72 -->
