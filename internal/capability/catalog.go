package capability

// arch is the sandbox's architecture as Go, Java and others name their
// downloads.
const arch = `arch=$(dpkg --print-architecture 2>/dev/null || uname -m | sed 's/x86_64/amd64/; s/aarch64/arm64/')`

// Catalog is every capability a sandbox can be given. Install scripts run
// as the agent, without root, so toolchains go in its home: binaries are
// linked into ~/.local/bin, which is on the PATH. They must be safe to run
// again, and link what they install every time: a later sandbox gets the
// Paths unpacked and runs Install only for its links and checks. Node 24
// with npm and Python 3 are part of the sandbox image.
var Catalog = []Capability{
	{
		ID:   "expo",
		Name: "Expo (React Native)",
		Description: "Mobile apps with Expo and React Native. Checks work with TypeScript, Jest and Metro bundles; " +
			"builds and phone previews go through EAS in Expo's cloud",
		Install: `# EAS CLI, for builds and updates in Expo's cloud. Node and npm come with the sandbox.
if ! command -v eas >/dev/null; then
  npm install --global --prefix "$HOME/.local" --no-fund --no-audit --loglevel=error eas-cli
fi`,
		Prepare: `# The app's packages, once it has any.
if [ -f package-lock.json ]; then npm ci --no-fund --no-audit; elif [ -f package.json ]; then npm install --no-fund --no-audit; fi`,
		Detect:  `grep -qsE '"(expo|react-native)"[[:space:]]*:' package.json || grep -qsE '"expo"[[:space:]]*:' app.json`,
		Paths:   []string{".local/lib/node_modules/eas-cli", ".local/bin/eas"},
		Secrets: []Secret{{Name: "EXPO_TOKEN", Why: "an Expo access token, for EAS builds, updates and submissions"}},
		CPUs:    2, MemoryMB: 4096,
		Skill: `# Expo and React Native in this sandbox

The sandbox is a Linux container with Node 24. Write and check the app here; native builds and phone
previews run in Expo's cloud with EAS. There is no simulator, emulator or device: don't try to run the
app, check it with the commands below instead.

## Starting an app
- ` + "`npx create-expo-app@latest`" + ` refuses a directory that already has files, such as a README. In a
  repository that isn't empty, create the app elsewhere and copy it in without overwriting:
  ` + "`npx create-expo-app@latest /tmp/app --yes && cp -rn /tmp/app/. . && rm -rf /tmp/app`" + `. Then add the
  template's .gitignore entries that are missing from the repository's.
- Use the latest SDK unless the project pins one.

## Dependencies
- Add packages with ` + "`npx expo install <package>`" + ` so their versions match the SDK; use npm only for
  packages Expo doesn't know. Run ` + "`npx expo-doctor`" + ` after changing dependencies.

## Checking your work
- Types: ` + "`npx tsc --noEmit`" + `.
- Tests: Jest with the jest-expo preset (` + "`npx expo install jest-expo jest @types/jest -- --save-dev`" + `), then
  ` + "`npx jest`" + `.
- Lint: ` + "`npx eslint .`" + `. If ` + "`npx expo lint`" + ` complains about the ESLint version, run ESLint directly.
- Bundles: ` + "`npx expo export --platform ios --output-dir /tmp/expo-export`" + `, and the same for android and web,
  bundle the app with Metro as a release build would and catch broken imports and missing modules.
  Keep the output out of the repository.

## Builds and previews with EAS
- EAS needs ` + "`EXPO_TOKEN`" + ` (an Expo access token, added as a secret in Beethoven). Check
  ` + "`[ -n \"$EXPO_TOKEN\" ]`" + ` first; without it, skip EAS and say in your final message that it needs the token.
- Link the project once with ` + "`eas init --non-interactive --force`" + ` when app.json has no
  extra.eas.projectId, and commit the change.
- Build without waiting: ` + "`eas build --platform all --non-interactive --no-wait`" + `, and report the build links
  it prints. iOS builds need Apple credentials set up in the Expo account; if they're missing, build
  Android only and say so.
- Preview on a phone: ` + "`eas update --branch \"$(git branch --show-current)\" --message \"<summary>\" --non-interactive`" + `
  publishes the JavaScript to this branch. Share the link it prints; it opens in a development build, or
  in Expo Go for apps without custom native code.
- Never submit to the App Store or Google Play (` + "`eas submit`" + `) unless you were asked to.

## Local Android builds
- They need the android capability: call add_capability with "android", then follow its skill.
  Local iOS builds are impossible on Linux; use EAS.`,
	},
	{
		ID:   "android",
		Name: "Android SDK",
		Description: "Local Android builds with Gradle. Installs JDK 17 and the Android SDK; " +
			"Gradle fetches the platform, build tools and NDK a project pins",
		Env: map[string]string{"JAVA_HOME": "$HOME/.local/jdk-17", "ANDROID_HOME": "$HOME/android-sdk"},
		Install: `# JDK 17 and the Android SDK, in the agent's home.
` + arch + `
jdk="$HOME/.local/jdk-17" sdk="$HOME/android-sdk"
mkdir -p "$HOME/.local/bin"
if [ ! -x "$jdk/bin/java" ]; then
  case $arch in arm64) a=aarch64 ;; *) a=x64 ;; esac
  rm -rf "$jdk" && mkdir -p "$jdk"
  curl -fsSL "https://api.adoptium.net/v3/binary/latest/17/ga/linux/$a/jdk/hotspot/normal/eclipse" | tar -xz -C "$jdk" --strip-components=1
fi
ln -sf "$jdk/bin/java" "$jdk/bin/javac" "$jdk/bin/keytool" "$jdk/bin/jar" "$HOME/.local/bin/"
# The command-line tools, at the last release that runs on Java alone (later ones are x86-64 only).
if [ ! -x "$sdk/cmdline-tools/latest/bin/sdkmanager" ]; then
  tmp=$(mktemp -d)
  curl -fsSL -o "$tmp/tools.zip" https://dl.google.com/android/repository/commandlinetools-linux-13114758_latest.zip
  unzip -q "$tmp/tools.zip" -d "$tmp"
  mkdir -p "$sdk/cmdline-tools" && rm -rf "$sdk/cmdline-tools/latest" && mv "$tmp/cmdline-tools" "$sdk/cmdline-tools/latest"
  rm -rf "$tmp"
fi
export JAVA_HOME="$jdk" ANDROID_HOME="$sdk"
# Accepting the licenses lets Gradle fetch the platform, build tools and NDK each project pins.
yes 2>/dev/null | "$sdk/cmdline-tools/latest/bin/sdkmanager" --licenses >/dev/null
"$sdk/cmdline-tools/latest/bin/sdkmanager" platform-tools >/dev/null
ln -sf "$sdk/cmdline-tools/latest/bin/sdkmanager" "$sdk/platform-tools/adb" "$HOME/.local/bin/"`,
		Paths:  []string{".local/jdk-17", "android-sdk"},
		Detect: `[ -f android/build.gradle ] || [ -f android/build.gradle.kts ] || [ -f android/settings.gradle ] || [ -f android/settings.gradle.kts ]`,
		CPUs:   4, MemoryMB: 8192,
		Skill: `# Android builds in this sandbox

JDK 17 and the Android SDK are installed; JAVA_HOME and ANDROID_HOME are set, and java, sdkmanager and
adb are on the PATH. Gradle downloads further SDK packages it needs (an NDK, CMake) by itself: the
licenses are accepted. There is no emulator or device, so build and test, but don't try to run the app.

- Expo apps: generate the native project with ` + "`npx expo prebuild --platform android --no-install`" + ` only when
  the project already keeps an android/ directory or you were asked to; otherwise prefer EAS builds
  (see the expo skill), since prebuild output is usually not committed.
- Build: ` + "`cd android && ./gradlew assembleDebug`" + ` (or ` + "`assembleRelease`" + `); unit tests: ` + "`./gradlew test`" + `.
  The first build downloads Gradle, the NDK and dependencies (several GB) and takes a while.
- React Native compiles native code for four CPU architectures by default. To check that a debug build
  works, build one: ` + "`./gradlew assembleDebug -PreactNativeArchitectures=x86_64`" + `.
- If the Gradle daemon dies or runs out of memory, build with ` + "`--max-workers=2`" + ` and set
  ` + "`org.gradle.jvmargs=-Xmx3g`" + ` in android/gradle.properties (without committing that unless asked).
- Release builds need a signing key: never generate or commit one unless asked.`,
	},
	{
		ID:          "go",
		Name:        "Go",
		Description: "The latest Go toolchain, and the project's modules",
		Install: `# The latest Go, in the agent's home.
if [ ! -x "$HOME/.local/go/bin/go" ]; then
  ` + arch + `
  v=$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -1)
  mkdir -p "$HOME/.local"
  curl -fsSL "https://go.dev/dl/$v.linux-$arch.tar.gz" | tar -xz -C "$HOME/.local"
fi
mkdir -p "$HOME/.local/bin"
ln -sf "$HOME/.local/go/bin/go" "$HOME/.local/go/bin/gofmt" "$HOME/.local/bin/"`,
		Paths:   []string{".local/go"},
		Prepare: `if [ -f go.mod ]; then go mod download; fi`,
		Detect:  `[ -f go.mod ]`,
		Skill: `# Go in this sandbox

The latest Go is installed in ~/.local/go, with go and gofmt on the PATH. Modules are cached in ~/go.
Before you finish, run ` + "`gofmt -l .`" + `, ` + "`go vet ./...`" + ` and ` + "`go test ./...`" + ` in each module you changed.`,
	},
	{
		ID:          "bun",
		Name:        "Bun",
		Description: "The Bun runtime and package manager, and the project's packages",
		Install: `# Bun, in the agent's home.
if [ ! -x "$HOME/.bun/bin/bun" ]; then
  curl -fsSL https://bun.sh/install | bash >/dev/null
fi
mkdir -p "$HOME/.local/bin"
ln -sf "$HOME/.bun/bin/bun" "$HOME/.local/bin/bun"
ln -sf "$HOME/.bun/bin/bun" "$HOME/.local/bin/bunx"`,
		Paths:   []string{".bun"},
		Prepare: `if [ -f package.json ]; then bun install; fi`,
		Detect:  `[ -f bun.lock ] || [ -f bun.lockb ] || [ -f bunfig.toml ]`,
		Skill: `# Bun in this sandbox

Bun is installed, with bun and bunx on the PATH. Install packages with ` + "`bun add`" + ` and run scripts with
` + "`bun run <script>`" + `; keep the bun.lock file in sync and commit it.`,
	},
	{
		ID:          "rust",
		Name:        "Rust",
		Description: "Stable Rust with Cargo, Clippy and rustfmt, and the project's crates",
		Install: `# Stable Rust, in the agent's home.
if [ ! -x "$HOME/.cargo/bin/cargo" ]; then
  curl -fsSL https://sh.rustup.rs | sh -s -- -y -q --profile minimal -c clippy,rustfmt --no-modify-path
fi
mkdir -p "$HOME/.local/bin"
ln -sf "$HOME"/.cargo/bin/* "$HOME/.local/bin/"`,
		Paths:   []string{".cargo", ".rustup"},
		Prepare: `if [ -f Cargo.toml ]; then cargo fetch; fi`,
		Detect:  `[ -f Cargo.toml ]`,
		CPUs:    2, MemoryMB: 4096,
		Skill: `# Rust in this sandbox

Stable Rust is installed with rustup (minimal profile plus Clippy and rustfmt); cargo, rustc and rustup
are on the PATH. Before you finish, run ` + "`cargo fmt --check`" + `, ` + "`cargo clippy --all-targets`" + ` and ` + "`cargo test`" + `.
Tools installed with ` + "`cargo install`" + ` land in ~/.cargo/bin, which is not on the PATH: call them by full path.`,
	},
	{
		ID:          "python",
		Name:        "Python with uv",
		Description: "uv for Python projects, and the project's dependencies. Python 3 comes with the sandbox",
		Install: `# uv, in the agent's home; Python 3 comes with the sandbox.
if ! command -v uv >/dev/null; then
  curl -fsSL https://astral.sh/uv/install.sh | env UV_NO_MODIFY_PATH=1 sh >/dev/null
fi`,
		Paths:   []string{".local/bin/uv", ".local/bin/uvx"},
		Prepare: `if [ -f pyproject.toml ]; then uv sync; elif [ -f requirements.txt ]; then python3 -m pip install -q -r requirements.txt; fi`,
		Detect:  `[ -f uv.lock ] || [ -f pyproject.toml ]`,
		Skill: `# Python with uv in this sandbox

uv is on the PATH. In a project with a pyproject.toml, add dependencies with ` + "`uv add`" + `, run things with
` + "`uv run <command>`" + ` (it uses the project's .venv), and keep uv.lock committed. Without one,
` + "`python3 -m pip install`" + ` installs into your user site.`,
	},
}
