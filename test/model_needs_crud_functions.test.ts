import { it, describe, assert } from "vitest"
import { model, run, models } from "../src"
import { Store } from "../src/databases"
import { Model, Field } from "../src/decorators"
import { Create, Read } from "../src/methods"
import { Text, Number } from "../src/types"
import * as lodash from "lodash"

it("Model required and crud operations", async function () {
    let need_crud_model = model({
        name: "need_crud_model",
        database: Store,
        schema: {
            msg: {
                type: Text,
            },
        },
    })

    assert.equal(lodash.has(need_crud_model.methods, "create"), true)
    assert.equal(lodash.has(need_crud_model.methods, "read"), true)
    assert.equal(lodash.has(need_crud_model.methods, "count"), true)
    assert.equal(lodash.has(need_crud_model.methods, "test"), true)
    assert.equal(lodash.has(need_crud_model.methods, "update"), true)
    assert.equal(lodash.has(need_crud_model.methods, "delete"), true)
})
