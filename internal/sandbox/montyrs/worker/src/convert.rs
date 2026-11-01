//! Values crossing the boundary. Both directions keep integers exact (any
//! size) and dict order; Python-only types follow CPython's json.dumps:
//! tuples and namedtuples become arrays, everything else that JSON cannot
//! represent (set, bytes, NaN, objects) is rejected instead of coerced.

use std::str::FromStr;

use monty_types::{MontyObject, ObjectRef};
use num_bigint::BigInt;
use serde_json::{Number, Value};

/// Deeper values are rejected rather than recursing on the native stack.
const MAX_DEPTH: usize = 256;

pub enum ToJsonError {
    TooLarge(usize),
    Unserializable(String),
}

/// Encodes a value as JSON text, failing as soon as it exceeds max_bytes.
pub fn to_json(value: ObjectRef<'_>, max_bytes: usize) -> Result<String, ToJsonError> {
    let mut w = Writer { out: String::new(), max_bytes };
    w.value(value, 0)?;
    Ok(w.out)
}

struct Writer {
    out: String,
    max_bytes: usize,
}

impl Writer {
    fn push(&mut self, s: &str) -> Result<(), ToJsonError> {
        self.out.push_str(s);
        if self.out.len() > self.max_bytes {
            return Err(ToJsonError::TooLarge(self.out.len()));
        }
        Ok(())
    }

    fn value(&mut self, v: ObjectRef<'_>, depth: usize) -> Result<(), ToJsonError> {
        if depth > MAX_DEPTH {
            return Err(ToJsonError::Unserializable(format!("value is nested deeper than {MAX_DEPTH} levels")));
        }
        match v.type_name() {
            "NoneType" => self.push("null"),
            "bool" => self.push(if v.as_bool() == Some(true) { "true" } else { "false" }),
            "int" => self.push(&int_text(v)),
            "float" => self.push(&float_text(v.as_float().unwrap_or(f64::NAN))?),
            "str" => self.push(&quote(v.as_str().unwrap_or_default())),
            "list" | "tuple" | "namedtuple" => self.array(v.items().unwrap_or_default(), depth),
            "dict" => self.object(v.pairs().unwrap_or_default(), depth),
            other => Err(not_serializable(other)),
        }
    }

    fn array(&mut self, items: Vec<ObjectRef<'_>>, depth: usize) -> Result<(), ToJsonError> {
        self.push("[")?;
        for (i, item) in items.into_iter().enumerate() {
            if i > 0 {
                self.push(",")?;
            }
            self.value(item, depth + 1)?;
        }
        self.push("]")
    }

    fn object(&mut self, pairs: Vec<(ObjectRef<'_>, ObjectRef<'_>)>, depth: usize) -> Result<(), ToJsonError> {
        self.push("{")?;
        for (i, (key, value)) in pairs.into_iter().enumerate() {
            if i > 0 {
                self.push(",")?;
            }
            self.push(&quote(&key_text(key)?))?;
            self.push(":")?;
            self.value(value, depth + 1)?;
        }
        self.push("}")
    }
}

/// Dict keys are converted like CPython's json.dumps does.
fn key_text(key: ObjectRef<'_>) -> Result<String, ToJsonError> {
    match key.type_name() {
        "str" => Ok(key.as_str().unwrap_or_default().to_owned()),
        "int" => Ok(int_text(key)),
        "float" => float_text(key.as_float().unwrap_or(f64::NAN)),
        "bool" => Ok(if key.as_bool() == Some(true) { "true" } else { "false" }.to_owned()),
        "NoneType" => Ok("null".to_owned()),
        other => Err(ToJsonError::Unserializable(format!(
            "keys must be str, int, float, bool or None, not {other}"
        ))),
    }
}

/// An int's decimal digits; repr() is exact for any size.
fn int_text(v: ObjectRef<'_>) -> String {
    v.as_int().map_or_else(|| v.py_repr(), |i| i.to_string())
}

fn float_text(f: f64) -> Result<String, ToJsonError> {
    if !f.is_finite() {
        return Err(ToJsonError::Unserializable(format!("Out of range float values are not JSON compliant: {f}")));
    }
    Ok(serde_json::to_string(&f).expect("finite floats serialize"))
}

fn quote(s: &str) -> String {
    serde_json::to_string(s).expect("strings serialize")
}

fn not_serializable(type_name: &str) -> ToJsonError {
    ToJsonError::Unserializable(format!("Object of type {type_name} is not JSON serializable"))
}

/// Decodes a bridge reply. Integers stay exact, objects keep their key order.
pub fn from_json(text: &str) -> Result<MontyObject, String> {
    let value: Value = serde_json::from_str(text).map_err(|e| e.to_string())?;
    Ok(to_object(value))
}

fn to_object(value: Value) -> MontyObject {
    match value {
        Value::Null => MontyObject::none(),
        Value::Bool(b) => MontyObject::bool(b),
        Value::Number(n) => number(&n),
        Value::String(s) => MontyObject::string(s),
        Value::Array(items) => MontyObject::list(items.into_iter().map(to_object)),
        Value::Object(map) => MontyObject::dict(map.into_iter().map(|(k, v)| (MontyObject::string(k), to_object(v)))),
    }
}

fn number(n: &Number) -> MontyObject {
    let text = n.to_string();
    if text.contains(['.', 'e', 'E']) {
        return MontyObject::float(text.parse().unwrap_or(f64::NAN));
    }
    match text.parse::<i64>() {
        Ok(i) => MontyObject::int(i),
        Err(_) => BigInt::from_str(&text).map_or_else(|_| MontyObject::float(f64::NAN), MontyObject::bigint),
    }
}
