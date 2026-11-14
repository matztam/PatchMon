# Signed SCAP content (fork)

`./build-signed.sh --ssg [SSG_VERSION]` downloads a ComplianceAsCode release,
checks its SHA-512 against the release, signs every `ssg-*-ds.xml` datastream
with the agent signing key and writes here:

- `VERSION`: the SSG release image builds download (pins `SSG_VERSION`).
- `ssg-*-ds.xml.minisig`: one signature per datastream, trusted comment
  `PatchMon SSG <version> <filename>`.

The image ships the signatures next to the datastreams. Agents only install a
datastream from the server when its signature verifies against the key built
into them, because remediation runs the fix scripts in it as root.
