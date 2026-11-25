# Homebrew formula for tmreportwell. This repository is its own tap.
#
# The release build rewrites the version line, the two sha256 lines by their
# marker comments and the bottle block between the BOTTLE markers. Keep the
# markers, and keep each url line directly above its sha256 line.
class Tmreportwell < Formula
  desc "Mail a daily report about Time Machine backups"
  homepage "https://github.com/moetiker/TmReportWell"
  version "0.0.2"
  license "MIT"

  # BOTTLE-START
  bottle do
    root_url "https://github.com/moetiker/TmReportWell/releases/download/v0.0.2"
    sha256 cellar: :any_skip_relocation, arm64_ventura: "08334ffa41a4ef819f1c40054de800d73fc6c3c28b285a06c71b47f772b83f3d"
  end
  # BOTTLE-END

  depends_on :macos

  on_arm do
    url "https://github.com/moetiker/TmReportWell/releases/download/v#{version}/tmreportwell-#{version}-aarch64-apple-darwin.tar.gz"
    sha256 "d99dc5ecc5d9f63139ed487f2e9007e23b49bf7f9920c7ad9007d9817a5c47b7" # mac-arm
  end
  on_intel do
    url "https://github.com/moetiker/TmReportWell/releases/download/v#{version}/tmreportwell-#{version}-x86_64-apple-darwin.tar.gz"
    sha256 "ef26be5691328cfb16f6e442daba92dd7de03358e5a93365a886f668f6bbd199" # mac-x86
  end

  def install
    bin.install "tmreportwell"
    Dir["man/*.[1-9]"].each { |page| (man/"man#{page[-1]}").install page }
    pkgshare.install "config.example.json"
  end

  def caveats
    <<~EOS
      Set up the configuration and the keychain item as the README describes, then run:
        tmreportwell send
        tmreportwell install
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/tmreportwell --version")
  end
end
