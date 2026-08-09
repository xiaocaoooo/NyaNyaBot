use std::env;
use std::fs;
use std::path::{Path, PathBuf};

fn main() {
    let manifest_dir = PathBuf::from(env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR"));
    let generated = manifest_dir.join("generated/frontend");
    let webui_out = manifest_dir.join("../../webui/out");
    let placeholder = manifest_dir.join("frontend-placeholder");

    println!("cargo:rerun-if-changed={}", webui_out.join("index.html").display());
    println!("cargo:rerun-if-changed={}", webui_out.display());
    rerun_if_dir_changed(&placeholder);

    let using_placeholder = !webui_out.join("index.html").is_file();
    let source = if using_placeholder {
        println!(
            "cargo:warning=webui/out not found; embedding frontend-placeholder (run `pnpm build` in webui/ or `cargo xtask frontend` for the real UI)"
        );
        &placeholder
    } else {
        &webui_out
    };

    if generated.exists() {
        fs::remove_dir_all(&generated).expect("remove generated/frontend");
    }
    copy_dir_recursive(source, &generated).expect("copy frontend assets into generated/frontend");

    // Ensure critical routes exist even if a partial out/ was provided.
    ensure_file(
        &generated.join("index.html"),
        &placeholder.join("index.html"),
    );
    ensure_file(
        &generated.join("login/index.html"),
        &placeholder.join("login/index.html"),
    );
    ensure_file(
        &generated.join("plugins/index.html"),
        &placeholder.join("plugins/index.html"),
    );
    ensure_file(
        &generated.join("404.html"),
        &placeholder.join("404.html"),
    );
}

fn rerun_if_dir_changed(dir: &Path) {
    if !dir.exists() {
        println!("cargo:rerun-if-changed={}", dir.display());
        return;
    }
    let mut stack = vec![dir.to_path_buf()];
    while let Some(current) = stack.pop() {
        println!("cargo:rerun-if-changed={}", current.display());
        let Ok(entries) = fs::read_dir(&current) else {
            continue;
        };
        for entry in entries.flatten() {
            let path = entry.path();
            if path.is_dir() {
                stack.push(path);
            } else {
                println!("cargo:rerun-if-changed={}", path.display());
            }
        }
    }
}

fn ensure_file(dest: &Path, fallback: &Path) {
    if dest.is_file() {
        return;
    }
    if let Some(parent) = dest.parent() {
        fs::create_dir_all(parent).expect("create fallback parent");
    }
    fs::copy(fallback, dest).unwrap_or_else(|err| {
        panic!(
            "missing {} and failed to copy fallback {}: {err}",
            dest.display(),
            fallback.display()
        )
    });
}

fn copy_dir_recursive(src: &Path, dst: &Path) -> std::io::Result<()> {
    fs::create_dir_all(dst)?;
    for entry in fs::read_dir(src)? {
        let entry = entry?;
        let ty = entry.file_type()?;
        let from = entry.path();
        let to = dst.join(entry.file_name());
        if ty.is_dir() {
            copy_dir_recursive(&from, &to)?;
        } else if ty.is_file() {
            if let Some(parent) = to.parent() {
                fs::create_dir_all(parent)?;
            }
            fs::copy(&from, &to)?;
        }
    }
    Ok(())
}
