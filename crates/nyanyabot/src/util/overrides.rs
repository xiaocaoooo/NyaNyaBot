use regex::Regex;
use serde::{Deserialize, Serialize};

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
}
