use std::collections::HashMap;

use regex::Regex;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq, Eq)]
pub struct OverrideRule {
    pub pattern: String,
    pub replacement: String,
}

pub fn apply_overrides(input: &str, overrides: &[OverrideRule]) -> String {
    for ov in overrides {
        let Ok(re) = Regex::new(&ov.pattern) else {
            continue;
        };
        if re.is_match(input) {
            return re.replace_all(input, ov.replacement.as_str()).into_owned();
        }
    }
    input.to_string()
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CommandPattern {
    pub id: String,
    pub name: String,
    pub pattern: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct OverrideMatchInfo {
    pub command_id: String,
    pub command_name: String,
    pub groups: HashMap<String, String>,
}

pub fn match_command_after_override(
    text: &str,
    commands: &[CommandPattern],
) -> Option<OverrideMatchInfo> {
    for cmd in commands {
        let Ok(re) = Regex::new(&cmd.pattern) else {
            continue;
        };
        let Some(caps) = re.captures(text) else {
            continue;
        };
        let mut groups = HashMap::new();
        for name in re.capture_names().flatten() {
            if let Some(m) = caps.name(name) {
                groups.insert(name.to_string(), m.as_str().to_string());
            }
        }
        return Some(OverrideMatchInfo {
            command_id: cmd.id.clone(),
            command_name: cmd.name.clone(),
            groups,
        });
    }
    None
}

pub fn test_override_response(
    input: &str,
    overrides: &[OverrideRule],
    commands: &[CommandPattern],
) -> Value {
    let result = apply_overrides(input, overrides);
    let match_info = match_command_after_override(&result, commands);
    json!({
        "result": result,
        "match_info": match_info,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn applies_first_match() {
        let rules = vec![
            OverrideRule {
                pattern: r"^foo$".into(),
                replacement: "bar".into(),
            },
            OverrideRule {
                pattern: r"^foo$".into(),
                replacement: "baz".into(),
            },
        ];
        assert_eq!(apply_overrides("foo", &rules), "bar");
    }

    #[test]
    fn matches_command_groups() {
        let commands = vec![CommandPattern {
            id: "cmd.echo".into(),
            name: "echo".into(),
            pattern: r"^echo (?P<content>.+)$".into(),
        }];
        let info = match_command_after_override("echo hi", &commands).unwrap();
        assert_eq!(info.command_id, "cmd.echo");
        assert_eq!(info.groups.get("content").unwrap(), "hi");
    }

    #[test]
    fn named_capture_replacement_go_parity() {
        let rules = vec![OverrideRule {
            pattern: r"^看看我的(?P<server>cn|jp|tw|en|kr)id是什么$".into(),
            replacement: "${server}id".into(),
        }];
        assert_eq!(apply_overrides("看看我的cnid是什么", &rules), "cnid");
    }
}
