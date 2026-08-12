//! NyaNyaBot host library.

pub mod app;
pub mod chatlog;
pub mod config;
pub mod configtmpl;
pub mod cron;
pub mod dedup;
pub mod dispatch;
pub mod onebot;
pub mod plugin;
pub mod pluginhost;
pub mod reaction;
pub mod stats;
pub mod triggerlog;
pub mod util;
pub mod web;

pub use app::App;
