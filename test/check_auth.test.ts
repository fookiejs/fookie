import * as lodash from "lodash"
import { it, describe, assert } from "vitest"
import { model, lifecycle, mixin } from "../packages/builder"
import { run } from "../packages/run"
import * as Database from "../packages/database"
import { Create, Read, Count, Delete, Test, Update } from "../packages/method"
import * as Type from "../packages/type"
import * as Mixin from "../packages/mixin"
import * as Fookie from "../index"
import * as Lifecycle from "../packages/lifecycle"

it("check auth 1", async function () {
    let child_setting = await model({
        name: "child_setting",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
            },
        },
        bind: {
            create: {
                role: [Lifecycle.nobody],
            },
        },
    })

    let create_res = await run({
        token: process.env.SYSTEM_TOKEN,
        model: child_setting,
        method: Create,
        body: {
            msg: "hola",
        },
    })
    assert.equal(create_res.status, false)
})

it("check auth 2", async function () {
    const child_setting2 = await model({
        name: "child_setting2",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
            },
        },
        bind: {
            create: {
                role: [Lifecycle.nobody],
            },
        },
    })

    let create_res_2 = await run({
        token: process.env.SYSTEM_TOKEN,
        model: child_setting2,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    assert.equal(create_res_2.status, false)
})

it(" check auth reject modify", async function () {
    let flag = false

    const test_r_modify = lifecycle(async function (payload, state) {
        flag = true
    })
    const msg_reject_1 = await model({
        name: "msg_reject_1",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
            },
        },
        bind: {
            create: {
                role: [Lifecycle.nobody],
                reject: {
                    nobody: {
                        modify: [test_r_modify],
                    },
                },
            },
        },
    })

    let create_res_2 = await run({
        token: process.env.SYSTEM_TOKEN,
        model: msg_reject_1,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    assert.equal(create_res_2.status, true)
    assert.equal(flag, true)
})

it(" check auth accept modify", async function () {
    let flag = false

    const test_a_modify = lifecycle(async function (payload, state) {
        flag = true
    })

    const msg_accept_0 = await model({
        name: "msg_accept_0",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
            },
        },
        bind: {
            create: {
                role: [Lifecycle.everybody],
                accept: {
                    everybody: {
                        modify: [test_a_modify],
                    },
                },
            },
        },
    })

    let create_res_2 = await run({
        model: msg_accept_0,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    assert.equal(create_res_2.status, true)
    assert.equal(flag, true)
})

it(" check auth array", async function () {
    let flag = false

    const test_a_modify = lifecycle(async function (payload, state) {
        flag = true
    })
    const msg_array = await model({
        name: "msg_array",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
            },
        },
        bind: {
            create: {
                role: [Lifecycle.nobody, Lifecycle.everybody],
            },
        },
    })

    let create_res_2 = await run({
        model: msg_array,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    assert.equal(create_res_2.status, true)
})

it(" check auth field write", async function () {
    const caf_role = lifecycle(async function (payload, state) {
        return false
    })

    const caf = await model({
        name: "caf",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
                write: [caf_role],
            },
        },
        bind: {
            create: {
                role: [],
            },
        },
    })

    let create_res_2 = await run({
        model: caf,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    assert.equal(create_res_2.status, false)
})

it(" check auth field read", async function () {
    const car_role = lifecycle(async function (payload, state) {
        return false
    })

    const car = await model({
        name: "car",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
                read: [car_role],
            },
        },
        bind: {
            create: {
                role: [Lifecycle.everybody],
            },
        },
    })

    await run({
        model: car,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    let read_res = await run({
        token: process.env.SYSTEM_TOKEN,
        model: car,
        method: Read,
        body: {
            msg: "hola",
        },
    })

    for (const data of read_res.data) {
        if (data.msg) throw Error("errrr")
    }
})

it(" check auth reject rule", async function () {
    let flag = false

    const test_r_rule = lifecycle(async function (payload, state) {
        flag = true
        return true
    })
    const msg_reject_2 = await model({
        name: "msg_reject_2",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
            },
        },
        bind: {
            create: {
                role: [Lifecycle.nobody],
                reject: {
                    nobody: {
                        rule: [test_r_rule],
                    },
                },
            },
        },
    })

    let create_res_2 = await run({
        token: process.env.SYSTEM_TOKEN,
        model: msg_reject_2,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    assert.equal(create_res_2.status, true)
    assert.equal(flag, true)
})

it(" check auth accept rule", async function () {
    let flag = false

    const test_a_rule = lifecycle(async function (payload, state) {
        flag = true
        return false
    })

    const msg_accept_1 = await model({
        name: "msg_accept_1",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
            },
        },
        bind: {
            create: {
                role: [Lifecycle.everybody],
                accept: {
                    everybody: {
                        rule: [test_a_rule],
                    },
                },
            },
        },
    })

    let create_res_2 = await run({
        token: process.env.SYSTEM_TOKEN,
        model: msg_accept_1,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    assert.equal(create_res_2.status, false)
    assert.equal(flag, true)
})

it(" check auth reject rule 2", async function () {
    const test_a_rule_3 = lifecycle(async function (payload, state) {
        return false
    })

    const msg_reject_3 = await model({
        name: "msg_reject_3",
        database: Database.Store,
        schema: {
            msg: {
                type: Type.Text,
            },
        },
        bind: {
            create: {
                role: [Lifecycle.nobody, Lifecycle.everybody],
                reject: {
                    nobody: {
                        rule: [test_a_rule_3],
                    },
                },
            },
        },
    })

    let create_res_2 = await run({
        token: process.env.SYSTEM_TOKEN,
        model: msg_reject_3,
        method: Create,
        body: {
            msg: "hola",
        },
    })

    assert.equal(create_res_2.status, false)
})
