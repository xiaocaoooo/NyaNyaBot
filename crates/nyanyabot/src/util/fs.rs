use std::path::{Path, PathBuf};

use anyhow::{Context, Result, bail};

pub fn ensure_dir(path: impl AsRef<Path>) -> Result<()> {
    let path = path.as_ref();
    if path.as_os_str().is_empty() {
        bail!("path is empty");
    }
    std::fs::create_dir_all(path).with_context(|| format!("mkdir {}", path.display()))?;
    Ok(())
}

pub fn workspace_data_dir() -> PathBuf {
    std::env::current_dir()
        .map(|p| p.join("data"))
        .unwrap_or_else(|_| PathBuf::from("data"))
}
