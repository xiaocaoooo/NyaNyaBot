use nyanyabot::config::{AccessControl, AppConfig, Store};
use tempfile::tempdir;

#[test]
fn access_and_store_smoke() {
    let ac = AccessControl {
        whitelist_users: vec![1],
        ..Default::default()
    };
    assert!(ac.allowed(1, 0));
    assert!(!ac.allowed(2, 0));

    let dir = tempdir().unwrap();
    let store = Store::new(dir.path()).unwrap();
    let cfg = store.load_or_create_default().unwrap();
    assert!(!cfg.webui.password.is_empty());
    let _ = AppConfig::default();
}
