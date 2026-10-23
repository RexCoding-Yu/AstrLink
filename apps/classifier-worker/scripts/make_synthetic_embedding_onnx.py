#!/usr/bin/env python3
"""Generate the <1 MiB synthetic embedding ONNX fixture.

The model looks each token up in a fixed 8-wide table and zeroes the padded
positions, so a token's hidden state depends on that token alone. Tests can
then tell CLS pooling (the first token) from mean pooling (all kept tokens).
"""

from pathlib import Path

import numpy as np
import onnx
from onnx import TensorProto, helper, numpy_helper

VOCABULARY = 8
DIMENSIONS = 8


def main() -> None:
    input_ids = helper.make_tensor_value_info(
        "input_ids", TensorProto.INT64, ["batch", "sequence"]
    )
    attention_mask = helper.make_tensor_value_info(
        "attention_mask", TensorProto.INT64, ["batch", "sequence"]
    )
    hidden = helper.make_tensor_value_info(
        "last_hidden_state", TensorProto.FLOAT, ["batch", "sequence", DIMENSIONS]
    )

    rng = np.random.default_rng(20261023)
    table = rng.normal(size=(VOCABULARY, DIMENSIONS)).astype(np.float32)
    table_init = numpy_helper.from_array(table, name="table")
    axes = numpy_helper.from_array(np.array([2], dtype=np.int64), name="axes")

    nodes = [
        helper.make_node("Gather", ["table", "input_ids"], ["looked_up"], axis=0),
        helper.make_node("Cast", ["attention_mask"], ["mask_f"], to=TensorProto.FLOAT),
        helper.make_node("Unsqueeze", ["mask_f", "axes"], ["mask_3d"]),
        helper.make_node("Mul", ["looked_up", "mask_3d"], ["last_hidden_state"]),
    ]
    graph = helper.make_graph(
        nodes,
        "astrlink_synthetic_embedding",
        [input_ids, attention_mask],
        [hidden],
        initializer=[table_init, axes],
    )
    model = helper.make_model(
        graph,
        producer_name="astrlink-synthetic-embedding",
        opset_imports=[helper.make_opsetid("", 18)],
    )
    model.ir_version = 10
    onnx.checker.check_model(model)
    output = Path(__file__).resolve().parents[1] / "testdata" / "synthetic-embedding.onnx"
    onnx.save(model, output)
    print(f"wrote {output} ({output.stat().st_size} bytes)")


if __name__ == "__main__":
    main()
