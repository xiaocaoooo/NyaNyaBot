use serde_json::Value;

/// Substitute `${global:NAME}` and `${env:NAME}` in JSON string values.
/// `\\${...}` becomes a literal `${...}`.
pub fn substitute_json(
    value: &Value,
    globals: &std::collections::HashMap<String, String>,
) -> (Value, bool) {
    match value {
        Value::String(s) => {
            let (out, changed) = substitute_string(s, globals);
            (Value::String(out), changed)
        }
        Value::Array(arr) => {
            let mut changed = false;
            let mut out = Vec::with_capacity(arr.len());
            for item in arr {
                let (v, c) = substitute_json(item, globals);
                changed |= c;
                out.push(v);
            }
            (Value::Array(out), changed)
        }
        Value::Object(map) => {
            let mut changed = false;
            let mut out = serde_json::Map::new();
            for (k, v) in map {
                let (nv, c) = substitute_json(v, globals);
                changed |= c;
                out.insert(k.clone(), nv);
            }
            (Value::Object(out), changed)
        }
        other => (other.clone(), false),
    }
}

pub fn substitute_string(
    s: &str,
    globals: &std::collections::HashMap<String, String>,
) -> (String, bool) {
    if s.is_empty() || (!s.contains("${") && !s.contains(r"\${")) {
        return (s.to_string(), false);
    }
    let bytes = s.as_bytes();
    let mut out = String::with_capacity(s.len());
    let mut changed = false;
    let mut i = 0;
    while i < bytes.len() {
        // Escape: \${...} => ${...}
        if bytes[i] == b'\\' && i + 2 < bytes.len() && bytes[i + 1] == b'$' && bytes[i + 2] == b'{'
        {
            if let Some(end) = find_closing_brace(s, i + 3) {
                out.push_str(&s[i + 1..end + 1]);
                i = end + 1;
                changed = true;
                continue;
            }
            out.push(bytes[i] as char);
            i += 1;
            continue;
        }
        if bytes[i] == b'$' && i + 1 < bytes.len() && bytes[i + 1] == b'{' {
            if let Some(end) = find_closing_brace(s, i + 2) {
                let name = &s[i + 2..end];
                if let Some(env_key) = name.strip_prefix("env:")
                    && is_valid_var_name(env_key)
                    && let Ok(val) = std::env::var(env_key)
                {
                    out.push_str(&val);
                    changed = true;
                    i = end + 1;
                    continue;
                } else if let Some(global_key) = name.strip_prefix("global:")
                    && is_valid_var_name(global_key)
                    && let Some(val) = globals.get(global_key)
                {
                    out.push_str(val);
                    changed = true;
                    i = end + 1;
                    continue;
                }
                out.push_str(&s[i..end + 1]);
                i = end + 1;
                continue;
            }
            out.push('$');
            i += 1;
            continue;
        }
        out.push(bytes[i] as char);
        i += 1;
    }
    if changed {
        (out, true)
    } else {
        (s.to_string(), false)
    }
}

fn find_closing_brace(s: &str, start: usize) -> Option<usize> {
    s[start..].find('}').map(|i| start + i)
}

fn is_valid_var_name(name: &str) -> bool {
    let mut chars = name.chars();
    match chars.next() {
        Some(c) if c == '_' || c.is_alphabetic() => {}
        _ => return false,
    }
    chars.all(|c| c == '_' || c.is_alphanumeric())
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    #[test]
    fn substitutes_global() {
        let mut g = HashMap::new();
        g.insert("token".into(), "abc".into());
        let (out, changed) = substitute_string("x=${global:token}", &g);
        assert!(changed);
        assert_eq!(out, "x=abc");
    }

    #[test]
    fn escapes_literal() {
        let g = HashMap::new();
        let (out, changed) = substitute_string(r"keep=\${global:token}", &g);
        assert!(changed);
        assert_eq!(out, "keep=${global:token}");
    }
}
