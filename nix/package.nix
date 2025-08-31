{
  lib,
  stdenv,
  fetchurl,
  autoPatchelfHook,
  glibc,
  zlib,
  openssl,
  version ? "0.0.611",
  releaseTag ? "open-chat-staging-0.0.610",
}:

let
  # SHA-256 hashes (SRI) of the official GitHub release assets.
  # Update along with `version`/`releaseTag` above (see ./update.sh).
  hashes = {
    x86_64-linux = "sha256-P19dykROTPv23uYWBw6zgMuDbVp7Bk9m7OlipfC2vNU=";
    aarch64-linux = "sha256-N98MBuBDWAp2OUih/l0JUDv+6xPeVj1BowRoVHt9zDk=";
    x86_64-darwin = "sha256-DeXzchXcUhhH5cNV0eGUWrI08WrCy8nUcyosOMEPmt8=";
    aarch64-darwin = "sha256-eRaVrubZB0LEGlhAGa3biwesG1UHwN42KDSOua+hw68=";
  };

  platforms = {
    x86_64-linux = {
      os = "linux";
      arch = "amd64";
    };
    aarch64-linux = {
      os = "linux";
      arch = "arm64";
    };
    x86_64-darwin = {
      os = "darwin";
      arch = "amd64";
    };
    aarch64-darwin = {
      os = "darwin";
      arch = "arm64";
    };
  };

  system = stdenv.hostPlatform.system;
  platform =
    platforms.${system}
      or (throw "open-chat: unsupported system ${system}");
  asset = "open-chat-${version}-${platform.os}-${platform.arch}";
in
stdenv.mkDerivation (finalAttrs: {
  pname = "open-chat";
  inherit version;

  src = fetchurl {
    url = "https://github.com/msgmate-io/open-chat-go/releases/download/${releaseTag}/${asset}";
    hash = hashes.${system};
  };

  # The release binaries are self-contained (frontend, routes.json, swagger and
  # the Vike build are embedded via //go:embed) but dynamically linked against
  # glibc on Linux. autoPatchelfHook rewrites the interpreter and rpath so the
  # binary runs on NixOS.
  nativeBuildInputs = lib.optionals stdenv.hostPlatform.isLinux [ autoPatchelfHook ];
  buildInputs = lib.optionals stdenv.hostPlatform.isLinux [
    glibc
    zlib
    openssl
    stdenv.cc.cc.lib
  ];

  dontUnpack = true;
  dontConfigure = true;
  dontBuild = true;

  installPhase = ''
    runHook preInstall

    install -Dm755 "$src" "$out/bin/open-chat"

    runHook postInstall
  '';

  passthru = {
    inherit releaseTag;
    updateScript = ./update.sh;
  };

  meta = {
    description = "Self-hosted chat server with integrated AI bots and tool integrations";
    homepage = "https://github.com/msgmate-io/open-chat-go";
    changelog = "https://github.com/msgmate-io/open-chat-go/releases";
    mainProgram = "open-chat";
    platforms = builtins.attrNames platforms;
    sourceProvenance = [ lib.sourceTypes.binaryNativeCode ];
  };
})