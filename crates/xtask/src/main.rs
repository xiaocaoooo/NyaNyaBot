use std::env;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

fn main() -> anyhow::Result<()> {
    let mut args = env::args().skip(1);
    let cmd = args.next().unwrap_or_else(|| "help".into());
    match cmd.as_str() {
        "stage" => stage(),
        "help" | "--help" | "-h" => {
            println!("xtask commands:\n  stage   build --release and copy binaries into plugins/");
            Ok(())
        }
        other => anyhow::bail!("unknown xtask command: {other}"),
    }
}

fn stage() -> anyhow::Result<()> {
    let status = Command::new("cargo")
        .args([
            "build",
            "--release",
            "-p",
            "nyanyabot",
            "-p",
            "nyanyabot-plugin-builtin-status",
            "-p",
            "nyanyabot-plugin-echo",
        ])
        .status()?;
    if !status.success() {
        anyhow::bail!("cargo build failed");
    }

    let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR")?);
    let workspace = manifest_dir.parent().unwrap().parent().unwrap();
    let target = workspace.join("target/release");
    let plugins = workspace.join("plugins");
    fs::create_dir_all(&plugins)?;

    let bins = [
        "nyanyabot",
        "nyanyabot-plugin-builtin-status",
        "nyanyabot-plugin-echo",
    ];
    for bin in bins {
        let mut src = target.join(bin);
        let mut dst_name = bin.to_string();
        if cfg!(windows) {
            src.set_extension("exe");
            if !dst_name.ends_with(".exe") {
                dst_name.push_str(".exe");
            }
        }
        if bin == "nyanyabot" {
            let dst = workspace.join(if cfg!(windows) {
                "nyanyabot.exe"
            } else {
                "nyanyabot"
            });
            copy_file(&src, &dst)?;
            continue;
        }
        let dst = plugins.join(dst_name);
        copy_file(&src, &dst)?;
    }
    println!("staged binaries into {}", plugins.display());
    Ok(())
}

fn copy_file(src: &Path, dst: &Path) -> anyhow::Result<()> {
    fs::copy(src, dst)
        .map_err(|e| anyhow::anyhow!("copy {} -> {}: {e}", src.display(), dst.display()))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mut perms = fs::metadata(dst)?.permissions();
        perms.set_mode(0o755);
        fs::set_permissions(dst, perms)?;
    }
    println!("copied {} -> {}", src.display(), dst.display());
    Ok(())
}
