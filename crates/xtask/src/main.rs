use std::env;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;

fn main() -> anyhow::Result<()> {
    let mut args = env::args().skip(1);
    let cmd = args.next().unwrap_or_else(|| "help".into());
    match cmd.as_str() {
        "stage" => stage(),
        "frontend" | "prepare-frontend" => frontend(),
        "help" | "--help" | "-h" => {
            println!(
                "xtask commands:\n  \
frontend   pnpm build in webui/ (writes webui/out for rust embed)\n  \
stage      build --release and copy binaries into plugins/\n\n\
Production tip: run `cargo xtask frontend` before `cargo build`/`stage` so the real WebUI is embedded."
            );
            Ok(())
        }
        other => anyhow::bail!("unknown xtask command: {other}"),
    }
}

fn workspace_root() -> anyhow::Result<PathBuf> {
    let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR")?);
    Ok(manifest_dir
        .parent()
        .and_then(|p| p.parent())
        .ok_or_else(|| anyhow::anyhow!("cannot resolve workspace root"))?
        .to_path_buf())
}

fn frontend() -> anyhow::Result<()> {
    let workspace = workspace_root()?;
    let webui = workspace.join("webui");
    if !webui.join("package.json").is_file() {
        anyhow::bail!("webui/package.json not found at {}", webui.display());
    }
    let status = Command::new("pnpm")
        .arg("build")
        .current_dir(&webui)
        .status()
        .map_err(|err| anyhow::anyhow!("failed to spawn pnpm: {err}"))?;
    if !status.success() {
        anyhow::bail!("pnpm build failed");
    }
    let index = webui.join("out/index.html");
    if !index.is_file() {
        anyhow::bail!("pnpm build succeeded but {} is missing", index.display());
    }
    println!("frontend export ready at {}", webui.join("out").display());
    Ok(())
}

fn stage() -> anyhow::Result<()> {
    let workspace = workspace_root()?;
    let out_index = workspace.join("webui/out/index.html");
    if !out_index.is_file() {
        eprintln!(
            "warning: {} missing — release binary will embed frontend-placeholder.\n\
Run `cargo xtask frontend` first for the real WebUI.",
            out_index.display()
        );
    }

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
            "-p",
            "nyanyabot-plugin-cron",
            "-p",
            "nyanyabot-plugin-configdump",
        ])
        .current_dir(&workspace)
        .status()?;
    if !status.success() {
        anyhow::bail!("cargo build failed");
    }

    let target = workspace.join("target/release");
    let plugins = workspace.join("plugins");
    fs::create_dir_all(&plugins)?;

    let bins = [
        "nyanyabot",
        "nyanyabot-plugin-builtin-status",
        "nyanyabot-plugin-echo",
        "nyanyabot-plugin-cron",
        "nyanyabot-plugin-configdump",
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
